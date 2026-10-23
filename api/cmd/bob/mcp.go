package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/badcodetv/bob/internal/mcp"
	"github.com/badcodetv/bob/internal/store"
)

// mcpToken is a chat's bearer token for Bob's MCP server: its session id and an HMAC of it under
// BOB_SESSION_SECRET. Every turn carries it. An agent can read its own chat's token, which only
// lets it act as that chat — which it already is.
func mcpToken(secret []byte, sessionID string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("bob-mcp\x00" + sessionID))
	return sessionID + "." + hex.EncodeToString(mac.Sum(nil))
}

// verifyMCPToken returns the session a token was minted for, if it was minted under secret.
func verifyMCPToken(secret []byte, token string) (sessionID string, ok bool) {
	id, _, found := strings.Cut(token, ".")
	if !found || id == "" {
		return "", false
	}
	if !hmac.Equal([]byte(token), []byte(mcpToken(secret, id))) {
		return "", false
	}
	return id, true
}

// mcpCaller is /mcp's auth: the chat the bearer token names, and from it the project, worker and
// the person (or schedule) whose message the agent is working on. A project's MCP key is the other
// credential: an outside caller with no chat, who reaches Bob at its public URL.
func (a *app) mcpCaller(r *http.Request) (mcp.Caller, error) {
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		return mcp.Caller{}, mcp.ErrUnauthorized
	}
	if project := a.mcpKeyProject(token); project != "" {
		user := "key:" + project
		if label := clientLabel(r.Header.Get("X-Bob-Client")); label != "" {
			user += " (" + label + ")"
		}
		return mcp.Caller{Project: project, User: user, APIBase: a.publicURL}, nil
	}
	id, ok := verifyMCPToken(a.auth.Secret, token)
	if !ok {
		return mcp.Caller{}, mcp.ErrUnauthorized
	}
	sess, err := a.store.Session(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return mcp.Caller{}, mcp.ErrNoSession
	}
	if err != nil {
		return mcp.Caller{}, err
	}
	user, err := a.store.LastUserEmail(r.Context(), id)
	if err != nil {
		return mcp.Caller{}, err
	}
	return mcp.Caller{Project: sess.Project, SessionID: sess.ID, Worker: sess.Worker, User: user, APIBase: "http://" + r.Host}, nil
}

// mcpKeyProject is the project whose MCP key token is, or "". Every key is compared, in constant
// time, so how long the answer takes says nothing about which one nearly matched.
func (a *app) mcpKeyProject(token string) string {
	found := ""
	for project, key := range a.mcpKeys {
		if hmac.Equal([]byte(token), []byte(key)) {
			found = project
		}
	}
	return found
}

// clientLabel is an outside client's X-Bob-Client header, which it may send to say who it is
// ("kai/enc-website", say), kept to a short run of plain characters. The client chooses it, so it
// is a name, not proof: the key is the credential.
func clientLabel(h string) string {
	h = strings.TrimSpace(h)
	if len(h) > 60 || !clientLabelPattern.MatchString(h) {
		return ""
	}
	return h
}

var clientLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._@/:-]*$`)

// bobWhoami lets an agent ask which chat it is running in: the tools it calls after this all
// scope to the same project and session, since the token names them, not an argument.
var bobWhoami = mcp.Tool{
	Name:        "bob_whoami",
	ForKeys:     true,
	Description: "Returns the project, chat and worker this call is running in, and who it is running for (\"key:<project>\" and no chat when called from outside Bob with the project's MCP key).",
	Call: func(_ context.Context, c mcp.Caller, _ json.RawMessage) (any, error) {
		return map[string]string{"project": c.Project, "session": c.SessionID, "worker": c.Worker, "user": c.User}, nil
	},
}
