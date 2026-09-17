package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/badcodetv/bob/internal/secrets"
	"github.com/badcodetv/bob/internal/store"
)

// maxSecretBytes keeps a value to a sensible size for an environment variable.
const maxSecretBytes = 16 << 10

// projectSecrets decrypts a project's secrets for its container.
func (a *app) projectSecrets(ctx context.Context, project string) (map[string]string, error) {
	sealed, err := a.store.SealedSecrets(ctx, project)
	if err != nil || len(sealed) == 0 {
		return nil, err
	}
	if a.secrets == nil {
		return nil, errors.New("project " + project + " has secrets but BOB_SECRETS_KEY is not set")
	}
	out := map[string]string{}
	for _, s := range sealed {
		v, err := a.secrets.Open(project, s.Name, s.Nonce, s.Ciphertext)
		if err != nil {
			return nil, err
		}
		out[s.Name] = v
	}
	return out, nil
}

// listSecrets names a project's secrets. Values are never sent to the browser.
func (a *app) listSecrets(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.Secrets(r.Context(), r.PathValue("project"))
	if list == nil {
		list = []store.SecretInfo{}
	}
	a.mu.Lock()
	pending := a.pending[r.PathValue("project")]
	a.mu.Unlock()
	reply(w, map[string]any{"secrets": list, "enabled": a.secrets != nil, "pending": pending}, err)
}

func (a *app) setSecret(w http.ResponseWriter, r *http.Request) {
	project, name := r.PathValue("project"), r.PathValue("name")
	var body struct {
		Value string `json:"value"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch {
	case a.secrets == nil:
		http.Error(w, "secrets are off: set BOB_SECRETS_KEY (openssl rand -base64 32) and restart Bob", http.StatusServiceUnavailable)
		return
	case secrets.CheckName(name) != nil:
		http.Error(w, secrets.CheckName(name).Error(), http.StatusBadRequest)
		return
	case body.Value == "" || len(body.Value) > maxSecretBytes:
		http.Error(w, "a secret's value must be 1 to 16384 bytes", http.StatusBadRequest)
		return
	}
	if _, err := a.store.Project(r.Context(), project); err != nil {
		reply(w, nil, err)
		return
	}
	nonce, ciphertext, err := a.secrets.Seal(project, name, body.Value)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if err := a.store.SetSecret(r.Context(), project, name, nonce, ciphertext, a.auth.Email(r)); err != nil {
		reply(w, nil, err)
		return
	}
	a.applySecrets(w, r, project)
}

func (a *app) deleteSecret(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if err := a.store.DeleteSecret(r.Context(), project, r.PathValue("name")); err != nil {
		reply(w, nil, err)
		return
	}
	a.applySecrets(w, r, project)
}

// applySecrets recreates the project's container so it starts with the changed secrets — unless
// work is in progress there (a turn, a scheduled run, a git sync), which a restart would cut off.
// Then the change is pending, and applied as soon as that work ends.
func (a *app) applySecrets(w http.ResponseWriter, r *http.Request, project string) {
	a.mu.Lock()
	if a.pending == nil {
		a.pending = map[string]bool{}
	}
	a.pending[project] = true
	a.mu.Unlock()
	applied, err := a.applyWhenIdle(r.Context(), project)
	reply(w, map[string]bool{"applied": applied}, err)
}

// applyWhenIdle recreates a project's container if a change is pending and nothing is running
// there. The check and the recreate happen under one lock, so no turn can start in between.
func (a *app) applyWhenIdle(ctx context.Context, project string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.pending[project] || a.active[project] > 0 {
		return false, nil
	}
	if err := a.runtime.Recreate(ctx, project); err != nil {
		return false, err
	}
	delete(a.pending, project)
	return true, nil
}
