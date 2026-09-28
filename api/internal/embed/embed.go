// Package embed turns text into vectors for memory search: the OpenAI embeddings API in
// production, and Fake, a deterministic offline stand-in, in tests.
//
// Every vector is Dim wide, fixed by the memories.embedding column (migration 004). Vectors from
// two different models are not comparable and memories are embedded once, when written — so
// changing BOB_EMBEDDING_MODEL after memories exist quietly worsens search over the old ones.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Dim is the width of every vector, fixed by the database column vector(1536).
const Dim = 1536

// DefaultModel emits Dim dimensions natively.
const DefaultModel = "text-embedding-3-small"

// Embedder turns text into a Dim-wide vector. Implementations are safe for concurrent use.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

var errBlank = errors.New("embed: cannot embed blank text")

// check is the contract every vector must meet before it is stored or searched with.
func check(v []float32) error {
	if len(v) != Dim {
		return fmt.Errorf("embed: want %d dimensions, got %d", Dim, len(v))
	}
	for i, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return fmt.Errorf("embed: non-finite value at index %d", i)
		}
	}
	return nil
}

// OpenAI calls POST https://api.openai.com/v1/embeddings. Model "" is DefaultModel. It asks for
// Dim dimensions explicitly, so a larger -3 model is reduced by OpenAI to fit the column.
type OpenAI struct {
	Key, Model string
	endpoint   string // tests only; "" is the real API
}

var openAIClient = &http.Client{Timeout: 30 * time.Second}

func (o OpenAI) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errBlank
	}
	model, url := o.Model, o.endpoint
	if model == "" {
		model = DefaultModel
	}
	if url == "" {
		url = "https://api.openai.com/v1/embeddings"
	}
	body, _ := json.Marshal(map[string]any{"input": text, "model": model, "dimensions": Dim})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := openAIClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: openai: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("embed: openai: %w", err)
	}
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	switch err := json.Unmarshal(raw, &parsed); {
	case err != nil:
		return nil, fmt.Errorf("embed: openai returned %s: %s", resp.Status, snippet(raw))
	case parsed.Error != nil:
		return nil, fmt.Errorf("embed: openai returned %s: %s", resp.Status, parsed.Error.Message)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("embed: openai returned %s: %s", resp.Status, snippet(raw))
	case len(parsed.Data) == 0:
		return nil, errors.New("embed: openai returned no embedding")
	}
	v := parsed.Data[0].Embedding
	if err := check(v); err != nil {
		return nil, fmt.Errorf("%w (model %s)", err, model)
	}
	return v, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// Fake is a hashed bag of words: the same words (ignoring case and punctuation) give the same
// unit-length vector, and texts sharing words sit closer than texts sharing none. It is not
// semantic — it cannot find a paraphrase — only the plumbing a real embedder plugs into.
type Fake struct{}

func (Fake) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, errBlank
	}
	v := make([]float32, Dim)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		words = []string{strings.TrimSpace(text)}
	}
	for _, w := range words {
		// Two dimensions per word, each with a hash-derived sign, so unrelated words mostly
		// cancel rather than accumulate.
		for probe := byte(0); probe < 2; probe++ {
			h := fnv.New64a()
			h.Write([]byte{probe})
			h.Write([]byte(w))
			sum := h.Sum64()
			if sum&(1<<63) != 0 {
				v[sum%Dim]--
			} else {
				v[sum%Dim]++
			}
		}
	}
	var n float64
	for _, f := range v {
		n += float64(f) * float64(f)
	}
	if n == 0 { // every probe cancelled out
		v[0], n = 1, 1
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
	return v, nil
}
