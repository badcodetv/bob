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
	// Tokens is each project's runtime token, by project name (see TokenVar). Required.
	Tokens map[string]string
	// Wait is how long Ensure waits for a project's runtime server to answer. Zero means 30s.
	Wait time.Duration
}

// TokenVar names the variable holding a project's runtime token: the password its runtime server
// requires on every request, so an agent in another project's container cannot drive this one
// over the Docker network. Each project has its own random value, given to Bob under this name and
// to the project's container as BOB_RUNTIME_TOKEN. Bob puts it in the base URL, from which Go's
// HTTP client sends it as basic auth (and leaves it out of error messages).
//
// The name is upper-cased and - becomes _, because a shell variable cannot hold a -. Project names
// are lower-case letters, digits and -, so no two map to the same variable.
func TokenVar(project string) string {
	return "BOB_RUNTIME_TOKEN_" + strings.ToUpper(strings.ReplaceAll(project, "-", "_"))
}

type Manager struct{ cfg Config }

func NewManager(cfg Config) *Manager { return &Manager{cfg: cfg} }

// ContainerName is what a project's container is called in the compose file. It is also its
// hostname on Bob's network, and the name of its volume — so a project's name can never change.
func ContainerName(project string) string { return "bob-project-" + project }

// Ensure returns the base URL of the project's runtime server, once it answers /health. It cannot
// start anything: a container that is not running is a deploy problem, and says so.
func (m *Manager) Ensure(ctx context.Context, p store.Project) (string, error) {
	token, ok := m.cfg.Tokens[p.Name]
	if !ok {
		return "", fmt.Errorf("project %s has no runtime token: set %s", p.Name, TokenVar(p.Name))
	}
	host, ok := m.cfg.Hosts[p.Name]
	if !ok {
		host = fmt.Sprintf("%s:%d", ContainerName(p.Name), containerPort)
	}
	base := (&url.URL{Scheme: "http", User: url.UserPassword("bob", token), Host: host}).String()
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

// TurnLine is one line of the runtime's NDJSON turn stream.
type TurnLine struct {
	Engine           string          `json:"engine,omitempty"`
	Event            json.RawMessage `json:"event,omitempty"`
	Done             bool            `json:"done,omitempty"`
	HarnessSessionID string          `json:"harness_session_id,omitempty"`
	Error            string          `json:"error,omitempty"`
}

// TurnRequest is POST /turns (runtime/src/turn.ts). It carries everything the turn runs with:
// the runtime reads no config of its own.
type TurnRequest struct {
	SessionID string `json:"session_id"`
	Engine    string `json:"engine"`
	// Model and Effort are what the turn runs with: the session's override, else the worker's.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Tools is what the agent may use without asking (claude only); nil means the harness default.
	Tools []string `json:"tools,omitempty"`
	// SystemPrompt is appended to the harness's own; always sent, even when empty.
	SystemPrompt string `json:"system_prompt"`
	// MCPToken is the chat's bearer token for Bob's MCP server; empty until Bob serves one.
	MCPToken string `json:"mcp_token"`
	Text     string `json:"text"`
	Resume   string `json:"resume,omitempty"`
	// UserEmail and UserName are who the turn runs for: the signed-in person, or
	// "schedule:<id>" and the schedule's name for a scheduled turn.
	UserEmail string `json:"user_email"`
	UserName  string `json:"user_name"`
}

// File fetches /files<escapedPath> from the runtime: a file from the project's work folder, or a
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
