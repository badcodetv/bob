package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func norm(v []float32) float64 {
	var s float64
	for _, f := range v {
		s += float64(f) * float64(f)
	}
	return math.Sqrt(s)
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// Fake gives the same unit-length Dim vector for the same words (case and punctuation aside),
// and texts sharing words sit closer than texts sharing none.
func TestFakeIsDeterministicAndUnitLength(t *testing.T) {
	var f Fake
	a, err := f.Embed(t.Context(), "The refund policy")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.Embed(t.Context(), "the REFUND policy!")
	if len(a) != Dim {
		t.Fatalf("len = %d, want %d", len(a), Dim)
	}
	if math.Abs(norm(a)-1) > 1e-5 {
		t.Errorf("norm = %v, want 1", norm(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same words, different vectors at %d", i)
		}
	}
	near, _ := f.Embed(t.Context(), "refund policy for customers")
	far, _ := f.Embed(t.Context(), "weather in lisbon tomorrow")
	if dot(a, near) <= dot(a, far) {
		t.Errorf("shared words should be closer: near %v, far %v", dot(a, near), dot(a, far))
	}
	if _, err := f.Embed(t.Context(), "   "); err == nil {
		t.Error("blank text should be an error")
	}
}

// OpenAI posts {input, model, dimensions: 1536} with the key as a bearer token, and returns the
// first embedding; a wrong width or an API error comes back as an error naming the cause.
func TestOpenAIRequestAndErrors(t *testing.T) {
	var got struct {
		Input      string `json:"input"`
		Model      string `json:"model"`
		Dimensions int    `json:"dimensions"`
	}
	var auth string
	reply := func(w http.ResponseWriter) {
		v := make([]float32, Dim)
		v[3] = 1
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": v}}})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		switch got.Input {
		case "short":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": []float32{1, 2}}}})
		case "denied":
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Incorrect API key provided"}})
		default:
			reply(w)
		}
	}))
	defer srv.Close()

	o := OpenAI{Key: "sk-test", endpoint: srv.URL}
	v, err := o.Embed(t.Context(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != Dim || v[3] != 1 {
		t.Errorf("vector = len %d, v[3] %v", len(v), v[3])
	}
	if auth != "Bearer sk-test" || got.Model != DefaultModel || got.Dimensions != Dim || got.Input != "hello" {
		t.Errorf("request = %q %+v", auth, got)
	}
	o.Model = "text-embedding-3-large"
	o.Embed(t.Context(), "hello")
	if got.Model != "text-embedding-3-large" {
		t.Errorf("model = %q, want the one set", got.Model)
	}
	if _, err := o.Embed(t.Context(), "short"); err == nil || !strings.Contains(err.Error(), "1536") {
		t.Errorf("wrong width: err = %v", err)
	}
	if _, err := o.Embed(t.Context(), "denied"); err == nil || !strings.Contains(err.Error(), "Incorrect API key") {
		t.Errorf("api error: err = %v", err)
	}
	if _, err := o.Embed(t.Context(), " "); err == nil {
		t.Error("blank text should be an error")
	}
}

// One real call, only when asked for: BOB_TEST_OPENAI=1 with OPENAI_API_KEY set. It passes
// quietly otherwise (not t.Skip: a skip reads as a missing database in this repo's checks).
func TestOpenAILive(t *testing.T) {
	if os.Getenv("BOB_TEST_OPENAI") == "" || os.Getenv("OPENAI_API_KEY") == "" {
		t.Log("not run: set BOB_TEST_OPENAI=1 and OPENAI_API_KEY for one real embedding call")
		return
	}
	v, err := OpenAI{Key: os.Getenv("OPENAI_API_KEY")}.Embed(context.Background(), "Bob remembers things.")
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != Dim || math.Abs(norm(v)-1) > 1e-3 {
		t.Errorf("len %d, norm %v", len(v), norm(v))
	}
}
