// Package runtime starts project containers and talks to the runtime server inside them.
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/badcodetv/bob/internal/docker"
	"github.com/badcodetv/bob/internal/store"
)

const containerPort = 8080

type Config struct {
	DefaultImage string
	// Network, when set, puts project containers on this Docker network and reaches them by
	// container name (Bob's API runs in a container on the same network). When empty, the
	// runtime port is published on 127.0.0.1 (Bob's API runs on the host).
	Network string
	// PassEnv lists variables copied from Bob's own environment into every project container.
	PassEnv map[string]string
}

type Manager struct {
	docker *docker.Client
	cfg    Config
	mu     sync.Mutex // serialises container creation per Bob process
}

func NewManager(d *docker.Client, cfg Config) *Manager { return &Manager{docker: d, cfg: cfg} }

func ContainerName(project string) string { return "bob-project-" + project }

// Ensure returns the base URL of the project's runtime server, creating and starting the
// container if needed, and waiting until it answers /health.
func (m *Manager) Ensure(ctx context.Context, p store.Project) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := ContainerName(p.Name)
	st, err := m.docker.Inspect(ctx, name)
	if errors.Is(err, docker.ErrNotFound) {
		id, cerr := m.docker.Create(ctx, m.spec(p))
		if cerr != nil {
			return "", cerr
		}
		st = docker.ContainerState{ID: id}
	} else if err != nil {
		return "", err
	}
	if !st.Running {
		if err := m.docker.Start(ctx, st.ID); err != nil {
			return "", err
		}
		if st, err = m.docker.Inspect(ctx, name); err != nil {
			return "", err
		}
	}
	base := fmt.Sprintf("http://%s:%d", name, containerPort)
	if m.cfg.Network == "" {
		base = "http://127.0.0.1:" + st.LoopbackPort
	}
	return base, waitHealthy(ctx, base)
}

// Recreate removes the project's container (keeping its volume) so the next Ensure picks up
// changed settings.
func (m *Manager) Recreate(ctx context.Context, project string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.docker.Remove(ctx, ContainerName(project))
}

func (m *Manager) spec(p store.Project) docker.ContainerSpec {
	image := p.Image
	if image == "" {
		image = m.cfg.DefaultImage
	}
	env := []string{"BOB_REPO_URL=" + p.RepoURL, "BOB_REPO_REF=" + p.RepoRef, "BOB_REPO_SUBFOLDER=" + p.Subfolder}
	for k, v := range m.cfg.PassEnv {
		env = append(env, k+"="+v)
	}
	binds := []string{"bob-project-" + p.Name + ":/project"}
	if p.RepoMount != "" {
		binds = append(binds, p.RepoMount+":/seed:ro")
	}
	return docker.ContainerSpec{
		Name:            ContainerName(p.Name),
		Image:           image,
		Env:             env,
		Labels:          map[string]string{"bob.project": p.Name},
		Binds:           binds,
		Network:         m.cfg.Network,
		PublishLoopback: m.cfg.Network == "",
		Port:            containerPort,
	}
}

func waitHealthy(ctx context.Context, base string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
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
			return fmt.Errorf("project runtime never became healthy: %w", last)
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

func Workers(ctx context.Context, base string) ([]Worker, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/workers", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("runtime /workers: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	var out struct{ Workers []Worker }
	return out.Workers, json.NewDecoder(resp.Body).Decode(&out)
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
