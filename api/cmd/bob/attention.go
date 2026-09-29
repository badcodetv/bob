package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/badcodetv/bob/internal/store"
)

// attentionWebhookVar is the environment variable that holds a project's attention webhook, named
// like runtime.TokenVar names a project's token: BOB_ATTENTION_WEBHOOK_<NAME>.
func attentionWebhookVar(project string) string {
	return "BOB_ATTENTION_WEBHOOK_" + strings.ToUpper(strings.ReplaceAll(project, "-", "_"))
}

// webhookFor is the project's attention webhook URL, or "" when it has none.
func (a *app) webhookFor(project string) string {
	if a.attentionWebhook != nil {
		return a.attentionWebhook(project)
	}
	return os.Getenv(attentionWebhookVar(project))
}

// notifyAttention POSTs a request to the project's webhook, if it has one, in a goroutine. A failure
// is only logged: the request is stored either way, and the UI shows it.
func (a *app) notifyAttention(x store.Attention) {
	url := a.webhookFor(x.Project)
	if url == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"project": x.Project, "worker": x.Worker, "kind": x.Kind,
		"message": x.Message, "chat_url": a.chatURL(x.Project, x.SessionID)})
	a.webhooks.Add(1)
	go func() {
		defer a.webhooks.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			log.Printf("attention webhook for %s: %v", x.Project, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("attention webhook for %s: %v", x.Project, err)
			return
		}
		res.Body.Close()
		if res.StatusCode >= 300 {
			log.Printf("attention webhook for %s: %s", x.Project, res.Status)
		}
	}()
}

// attentionView is a request as the web app gets it.
type attentionView struct {
	store.Attention
	ChatURL string `json:"chat_url"`
}

func (a *app) attentionViewOf(x store.Attention) attentionView {
	return attentionView{Attention: x, ChatURL: a.chatURL(x.Project, x.SessionID)}
}

// listAttention is GET /api/projects/{project}/attention[?state=open].
func (a *app) listAttention(w http.ResponseWriter, r *http.Request) {
	xs, err := a.store.Attentions(r.Context(), r.PathValue("project"), r.URL.Query().Get("state") == "open")
	if err != nil {
		reply(w, nil, err)
		return
	}
	out := make([]attentionView, len(xs))
	for i, x := range xs {
		out[i] = a.attentionViewOf(x)
	}
	reply(w, map[string]any{"attention": out}, nil)
}

// dismissAttention is POST /api/attention/{attention}/dismiss: the person says it needs nothing.
func (a *app) dismissAttention(w http.ResponseWriter, r *http.Request) {
	x, err := a.store.CloseAttention(r.Context(), r.PathValue("attention"), a.auth.Email(r), "dismissed")
	if err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, a.attentionViewOf(x), nil)
}
