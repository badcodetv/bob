package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
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
// the person (or schedule) whose message the agent is working on.
func (a *app) mcpCaller(r *http.Request) (mcp.Caller, error) {
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		return mcp.Caller{}, mcp.ErrUnauthorized
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
