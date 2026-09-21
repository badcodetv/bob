// Package runtime talks to the runtime server inside each project's container.
//
// Bob does not start containers. Each project has one declared in the same deploy's compose file,
// always running, reached by its name on the Docker network — so Bob needs no Docker socket, and
// nothing in this package speaks to Docker.
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/badcodetv/bob/internal/store"
)

const containerPort = 8080

type Config struct {
	// Hosts overrides where a project's runtime server is, by project name. Local development
	// sets it, because a Bob running on the host cannot resolve container names.
	Hosts map[string]string
	// TokenKey derives each project's runtime token (see Token). Required.
	TokenKey []byte
	// Wait is how long Ensure waits for a project's runtime server to answer. Zero means 30s.
	Wait time.Duration
}

// Token is the password a project's runtime server requires on every request, so an agent in
// another project's container cannot drive this one over the Docker network. The compose file
// passes it to the container as BOB_RUNTIME_TOKEN; Bob derives the same value and puts it in the
// base URL, from which Go's HTTP client sends it as basic auth (and leaves it out of error
// messages). Bob and the compose generator must use the same BOB_RUNTIME_KEY.
func Token(key []byte, project string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("bob-runtime\x00" + project))
	return hex.EncodeToString(mac.Sum(nil))
}

type Manager struct{ cfg Config }

func NewManager(cfg Config) *Manager { return &Manager{cfg: cfg} }

// ContainerName is what a project's container is called in the compose file. It is also its
// hostname on Bob's network, and the name of its volume — so a project's name can never change.
func ContainerName(project string) string { return "bob-project-" + project }

// Ensure returns the base URL of the project's runtime server, once it answers /health. It cannot
// start anything: a container that is not running is a deploy problem, and says so.
func (m *Manager) Ensure(ctx context.Context, p store.Project) (string, error) {
	host, ok := m.cfg.Hosts[p.Name]
	if !ok {
		host = fmt.Sprintf("%s:%d", ContainerName(p.Name), containerPort)
	}
	base := (&url.URL{Scheme: "http", User: url.UserPassword("bob", Token(m.cfg.TokenKey, p.Name)), Host: host}).String()
	return base, m.waitHealthy(ctx, p.Name, base)
}

func (m *Manager) waitHealthy(ctx context.Context, project, base string) error {
	wait := m.cfg.Wait
	if wait == 0 {
		wait = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var last error
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("health: %s", resp.Status)
		}
		last = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("project %s is not answering: its container (%s) is not running, or not on Bob's network — check the deploy: %w",
				project, ContainerName(project), last)
		case <-time.After(300 * time.Millisecond):
		}
	}
}

type Worker struct {
	Name   string   `json:"name"`
	Engine string   `json:"engine"`
	Model  string   `json:"model,omitempty"`
	Effort string   `json:"effort,omitempty"`
	Tools  []string `json:"tools,omitempty"`
	Prompt string   `json:"prompt"`
}

// WorkerList is what the runtime reports about a project's config folder.
type WorkerList struct {
	Sync struct {
		OK     bool   `json:"ok"`
		Commit string `json:"commit,omitempty"`
		Error  string `json:"error,omitempty"`
	} `json:"sync"`
	Workers []Worker `json:"workers"`
	// Error is set when the folder was fetched but a worker file could not be read.
	Error string `json:"error,omitempty"`
}

// Workers lists the project's workers; with sync, it pulls the git folder first.
func Workers(ctx context.Context, base string, sync bool) (WorkerList, error) {
	method, path := http.MethodGet, "/workers"
	if sync {
		method, path = http.MethodPost, "/sync"
	}
	req, _ := http.NewRequestWithContext(ctx, method, base+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return WorkerList{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return WorkerList{}, fmt.Errorf("runtime %s: %s: %s", path, resp.Status, bytes.TrimSpace(msg))
	}
	var out WorkerList
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// TurnLine is one line of the runtime's NDJSON turn stream.
type TurnLine struct {
	Engine           string          `json:"engine,omitempty"`
	Event            json.RawMessage `json:"event,omitempty"`
	Done             bool            `json:"done,omitempty"`
	HarnessSessionID string          `json:"harness_session_id,omitempty"`
	Error            string          `json:"error,omitempty"`
}

type TurnRequest struct {
	SessionID string `json:"session_id"`
	Worker    string `json:"worker"`
	Text      string `json:"text"`
	Resume    string `json:"resume,omitempty"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	// UserEmail and UserName are who the turn runs for: the signed-in person, or
	// "schedule:<id>" and the schedule's name for a scheduled turn.
	UserEmail string `json:"user_email"`
	UserName  string `json:"user_name"`
}

// RemoveSession deletes a session's worktree and branch inside the project container.
func RemoveSession(ctx context.Context, base, sessionID string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/sessions/"+sessionID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("runtime delete session: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}

// File fetches /files<escapedPath> from the runtime: a file from the synced checkout, or a
// directory listing. The caller closes the body and passes the status on.
func File(ctx context.Context, base, escapedPath string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/files"+escapedPath, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// RunTurn posts a turn and calls onLine for every line until the runtime closes the stream.
func RunTurn(ctx context.Context, base string, t TurnRequest, onLine func(TurnLine) error) error {
	body, _ := json.Marshal(t)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/turns", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("runtime /turns: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for sc.Scan() {
		var line TurnLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			return fmt.Errorf("runtime sent a bad line: %w", err)
		}
		if err := onLine(line); err != nil {
			return err
		}
	}
	return sc.Err()
}
