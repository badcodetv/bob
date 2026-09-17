// Package runtime starts project containers and talks to the runtime server inside them.
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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
	// Secrets returns a project's own secrets, decrypted, when its container is created. They
	// are added after PassEnv, so a project's secret replaces a passed variable of the same name.
	Secrets func(ctx context.Context, project string) (map[string]string, error)
	// TokenKey derives each project's runtime token (see Token). Required.
	TokenKey []byte
	// Limits for every project container; zero means none.
	Memory    int64 // bytes
	NanoCPUs  int64
	PidsLimit int64
}

// Token is the password a project's runtime server requires on every request, so an agent in
// another project's container cannot drive this one over the Docker network. Bob passes it to the
// container as BOB_RUNTIME_TOKEN and puts it in the base URL, from which Go's HTTP client sends it
// as basic auth (and leaves it out of error messages).
func Token(key []byte, project string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("bob-runtime\x00" + project))
	return hex.EncodeToString(mac.Sum(nil))
}

type Manager struct {
	docker *docker.Client
	cfg    Config
	mu     sync.Mutex // serialises container creation per Bob process
	// destroyed names projects whose container and volume were removed: Ensure refuses them, so a
	// request that was already on its way cannot bring the container back (with the project's
	// secrets) after the project is deleted. Revive clears the mark for a re-created project.
	destroyed map[string]bool
}

// Revive allows a container again for a project name that was destroyed and has been created anew.
func (m *Manager) Revive(project string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.destroyed, project)
}

func NewManager(d *docker.Client, cfg Config) *Manager { return &Manager{docker: d, cfg: cfg} }

// SetSecrets sets Config.Secrets after construction (the source needs the app that holds the Manager).
func (m *Manager) SetSecrets(f func(ctx context.Context, project string) (map[string]string, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Secrets = f
}

func ContainerName(project string) string { return "bob-project-" + project }

// VolumeName is the project's volume: its repository checkout, chat worktrees and harness state.
func VolumeName(project string) string { return "bob-project-" + project }

// Ensure returns the base URL of the project's runtime server, creating and starting the
// container if needed, and waiting until it answers /health.
func (m *Manager) Ensure(ctx context.Context, p store.Project) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.destroyed[p.Name] {
		return "", fmt.Errorf("project %s has been deleted", p.Name)
	}
	name := ContainerName(p.Name)
	st, err := m.docker.Inspect(ctx, name)
	if errors.Is(err, docker.ErrNotFound) {
		var secrets map[string]string
		if m.cfg.Secrets != nil {
			if secrets, err = m.cfg.Secrets(ctx, p.Name); err != nil {
				return "", err
			}
		}
		id, cerr := m.docker.Create(ctx, m.spec(p, secrets))
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
	host := fmt.Sprintf("%s:%d", name, containerPort)
	if m.cfg.Network == "" {
		host = "127.0.0.1:" + st.LoopbackPort
	}
	base := (&url.URL{Scheme: "http", User: url.UserPassword("bob", Token(m.cfg.TokenKey, p.Name)), Host: host}).String()
	return base, waitHealthy(ctx, base)
}

// SpecLabel is the container label holding a fingerprint of everything Bob configured it with.
const SpecLabel = "bob.spec"

// ReplaceStale removes each project container whose configuration no longer matches what Bob
// would create now — a new runtime image, a changed pass-through variable (a rotated token), new
// limits — so the next turn starts a fresh one on the same volume. Call it at startup, before
// any turn runs: removing a container stops whatever it is doing.
func (m *Manager) ReplaceStale(ctx context.Context, projects []store.Project) (replaced []string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range projects {
		st, err := m.docker.Inspect(ctx, ContainerName(p.Name))
		if errors.Is(err, docker.ErrNotFound) {
			continue
		}
		if err != nil {
			return replaced, err
		}
		var secrets map[string]string
		if m.cfg.Secrets != nil {
			if secrets, err = m.cfg.Secrets(ctx, p.Name); err != nil {
				return replaced, err
			}
		}
		if st.Labels[SpecLabel] == m.spec(p, secrets).Labels[SpecLabel] {
			continue
		}
		if err := m.docker.Remove(ctx, ContainerName(p.Name)); err != nil {
			return replaced, err
		}
		replaced = append(replaced, p.Name)
	}
	return replaced, nil
}

// Recreate removes the project's container (keeping its volume) so the next Ensure picks up
// changed settings.
func (m *Manager) Recreate(ctx context.Context, project string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.docker.Remove(ctx, ContainerName(project))
}

// Destroy removes the project's container and its volume. Nothing of the project is left in Docker.
func (m *Manager) Destroy(ctx context.Context, project string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.destroyed == nil {
		m.destroyed = map[string]bool{}
	}
	m.destroyed[project] = true
	if err := m.docker.Remove(ctx, ContainerName(project)); err != nil {
		return err
	}
	return m.docker.RemoveVolume(ctx, VolumeName(project))
}

func (m *Manager) spec(p store.Project, secrets map[string]string) docker.ContainerSpec {
	image := p.Image
	if image == "" {
		image = m.cfg.DefaultImage
	}
	vars := map[string]string{}
	for k, v := range m.cfg.PassEnv {
		vars[k] = v
	}
	for k, v := range secrets {
		vars[k] = v
	}
	vars["BOB_REPO_URL"], vars["BOB_REPO_REF"], vars["BOB_REPO_SUBFOLDER"] = p.RepoURL, p.RepoRef, p.Subfolder
	vars["BOB_RUNTIME_TOKEN"] = Token(m.cfg.TokenKey, p.Name)
	env := make([]string, 0, len(vars))
	for k, v := range vars {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	binds := []string{VolumeName(p.Name) + ":/project"}
	if p.RepoMount != "" {
		binds = append(binds, p.RepoMount+":/seed:ro")
	}
	s := docker.ContainerSpec{
		Name:            ContainerName(p.Name),
		Image:           image,
		Env:             env,
		Labels:          map[string]string{"bob.project": p.Name},
		Binds:           binds,
		Network:         m.cfg.Network,
		PublishLoopback: m.cfg.Network == "",
		Port:            containerPort,
		Memory:          m.cfg.Memory,
		NanoCPUs:        m.cfg.NanoCPUs,
		PidsLimit:       m.cfg.PidsLimit,
	}
	// A keyed hash, not a plain one: the environment holds secrets, and labels are readable by
	// anyone who can list containers.
	canonical, _ := json.Marshal(s)
	mac := hmac.New(sha256.New, m.cfg.TokenKey)
	mac.Write([]byte("bob-spec\x00"))
	mac.Write(canonical)
	s.Labels[SpecLabel] = hex.EncodeToString(mac.Sum(nil))
	return s
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
