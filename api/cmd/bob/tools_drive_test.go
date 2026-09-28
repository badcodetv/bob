package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/bob/internal/drive"
	"github.com/badcodetv/bob/internal/mcp"
)

// newFakeDriveServer serves just enough of the OAuth token endpoint and the Drive v3 REST API for
// tools_drive.go's tests: one small file ("f1"), one long text file ("big") for paging, and one
// opaque file ("pdf1") for drive_fetch/Download.
func newFakeDriveServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fake", "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"files": []map[string]any{
				{"id": "f1", "name": "one", "mimeType": "text/plain", "modifiedTime": "2024-01-01T00:00:00Z"},
			},
			"nextPageToken": "",
		})
	})
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/files/"), "/export")
		alt := r.URL.Query().Get("alt")
		switch id {
		case "big":
			if alt == "media" {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte(strings.Repeat("0123456789", 10))) // 100 chars
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "big", "name": "big.txt", "mimeType": "text/plain"})
			return
		case "pdf1":
			if alt == "media" {
				w.Header().Set("Content-Type", "application/pdf")
				_, _ = w.Write([]byte("%PDF-fake-bytes"))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pdf1", "name": "report.pdf", "mimeType": "application/pdf"})
			return
		}
		http.Error(w, "unknown file "+id, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// driveToolsApp wires a's MCP server with the drive tools plus a fake Drive client for project
// "wolf" only; "dev" has no Drive client, to check drive_* is unavailable to it. Returns chat
// tokens for both projects.
func driveToolsApp(t *testing.T) (a *app, wolfToken, devToken string) {
	t.Helper()
	a, _, _ = newScheduleApp(t)
	if err := a.store.ReconcileProjects(t.Context(), []string{"wolf", "dev"}); err != nil {
		t.Fatal(err)
	}

	srv := newFakeDriveServer(t)
	cl, err := drive.New(context.Background(), "client-id", "client-secret", "refresh-token",
		drive.WithTokenEndpoint(srv.URL+"/token"), drive.WithAPIEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	a.drive = map[string]*drive.Client{"wolf": cl}

	a.mcp = mcp.New()
	a.mcp.Register(a.driveTools()...)

	wolfSess, err := a.store.CreateSession(t.Context(), "wolf", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userMessage(t, a, wolfSess, "kai@example.com")

	devSess, err := a.store.CreateSession(t.Context(), "dev", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	userMessage(t, a, devSess, "kai@example.com")

	return a, mcpToken(a.auth.Secret, wolfSess.ID), mcpToken(a.auth.Secret, devSess.ID)
}

// listTools calls tools/list and returns the listed tool names.
func listTools(t *testing.T, a *app, token string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Host = "api:8070"
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("tools/list = %d: %s", res.Code, res.Body)
	}
	var r struct {
		Result struct {
			Tools []struct{ Name string }
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &r); err != nil {
		t.Fatalf("decoding tools/list: %v: %s", err, res.Body)
	}
	names := make([]string, len(r.Result.Tools))
	for i, tool := range r.Result.Tools {
		names[i] = tool.Name
	}
	return names
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestDriveToolsHiddenWithoutADriveClient(t *testing.T) {
	a, _, devToken := driveToolsApp(t)
	names := listTools(t, a, devToken)
	for _, want := range []string{"drive_search", "drive_list", "drive_read", "drive_fetch"} {
		if containsStr(names, want) {
			t.Errorf("dev project (no Drive client) sees %s in tools/list: %v", want, names)
		}
	}
}

func TestDriveToolsVisibleWithADriveClient(t *testing.T) {
	a, wolfToken, _ := driveToolsApp(t)
	names := listTools(t, a, wolfToken)
	for _, want := range []string{"drive_search", "drive_list", "drive_read", "drive_fetch"} {
		if !containsStr(names, want) {
			t.Errorf("wolf project (has a Drive client) missing %s in tools/list: %v", want, names)
		}
	}
}

func TestDriveToolsNotCallableWithoutADriveClient(t *testing.T) {
	a, _, devToken := driveToolsApp(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"drive_search","arguments":{"query":"x"}}}`))
	req.Host = "api:8070"
	req.Header.Set("Authorization", "Bearer "+devToken)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("drive_search from dev = %d: %s", res.Code, res.Body)
	}
	var r struct {
		Error *struct{ Message string }
	}
	if err := json.Unmarshal(res.Body.Bytes(), &r); err != nil {
		t.Fatalf("decoding: %v: %s", err, res.Body)
	}
	if r.Error == nil || !strings.Contains(r.Error.Message, "no tool named") {
		t.Fatalf("want a JSON-RPC error naming the unknown tool, got %s", res.Body)
	}
}

func TestDriveReadPagesByOffsetAndLimit(t *testing.T) {
	a, wolfToken, _ := driveToolsApp(t)
	status, isErr, text := callTool(t, a, wolfToken, "drive_read", `{"file_id":"big","offset":10,"limit":5}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("drive_read = %d isError=%v %s", status, isErr, text)
	}
	var out struct {
		Name       string `json:"name"`
		MimeType   string `json:"mime_type"`
		Text       string `json:"text"`
		Offset     int    `json:"offset"`
		TotalChars int    `json:"total_chars"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decoding: %v: %s", err, text)
	}
	full := strings.Repeat("0123456789", 10)
	if out.Text != full[10:15] || out.Offset != 10 || out.TotalChars != 100 || out.Name != "big.txt" || out.MimeType != "text/plain" {
		t.Fatalf("got %+v", out)
	}
}

func TestDriveReadDefaultsToWholeFileWithinDefaultLimit(t *testing.T) {
	a, wolfToken, _ := driveToolsApp(t)
	status, isErr, text := callTool(t, a, wolfToken, "drive_read", `{"file_id":"big"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("drive_read = %d isError=%v %s", status, isErr, text)
	}
	var out struct {
		Text   string `json:"text"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Offset != 0 || out.Text != strings.Repeat("0123456789", 10) {
		t.Fatalf("got %+v", out)
	}
}

func TestDriveFetchMintsAWorkingURL(t *testing.T) {
	a, wolfToken, _ := driveToolsApp(t)
	status, isErr, text := callTool(t, a, wolfToken, "drive_fetch", `{"file_id":"pdf1"}`)
	if status != http.StatusOK || isErr {
		t.Fatalf("drive_fetch = %d isError=%v %s", status, isErr, text)
	}
	var out struct {
		URL       string    `json:"url"`
		Name      string    `json:"name"`
		MimeType  string    `json:"mime_type"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decoding: %v: %s", err, text)
	}
	if out.Name != "report.pdf" || out.MimeType != "application/pdf" {
		t.Fatalf("got %+v", out)
	}
	if !strings.HasPrefix(out.URL, "http://api:8070/drive/fetch/") {
		t.Fatalf("url = %q, want it built from the /mcp request's Host header", out.URL)
	}
	if time.Until(out.ExpiresAt) > 10*time.Minute || time.Until(out.ExpiresAt) < 9*time.Minute {
		t.Fatalf("expires_at = %v, want ~10 minutes out", out.ExpiresAt)
	}

	token := strings.TrimPrefix(out.URL, "http://api:8070/drive/fetch/")
	req := httptest.NewRequest(http.MethodGet, "/drive/fetch/"+token, nil)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /drive/fetch/{token} = %d: %s", res.Code, res.Body)
	}
	if got := res.Header().Get("Content-Disposition"); got != `attachment; filename="report.pdf"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "%PDF-fake-bytes" {
		t.Errorf("body = %q", body)
	}
}

func TestDriveFetchTamperedTokenIs403(t *testing.T) {
	a, wolfToken, _ := driveToolsApp(t)
	_, isErr, text := callTool(t, a, wolfToken, "drive_fetch", `{"file_id":"pdf1"}`)
	if isErr {
		t.Fatal(text)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(out.URL, "http://api:8070/drive/fetch/")
	// flip the last character of the signature: still well-formed, no longer valid.
	tampered := token[:len(token)-1] + flipHexDigit(token[len(token)-1])

	req := httptest.NewRequest(http.MethodGet, "/drive/fetch/"+tampered, nil)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("tampered token = %d, want 403", res.Code)
	}
}

func flipHexDigit(b byte) string {
	if b == '0' {
		return "1"
	}
	return "0"
}

func TestDriveFetchExpiredTokenIs403(t *testing.T) {
	a, _, _ := driveToolsApp(t)
	token := signDriveFetchToken(a.auth.Secret, "wolf", "pdf1", time.Now().Add(-time.Minute))

	req := httptest.NewRequest(http.MethodGet, "/drive/fetch/"+token, nil)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expired token = %d, want 403", res.Code)
	}
}

func TestDriveFetchGarbageTokenIs403(t *testing.T) {
	a, _, _ := driveToolsApp(t)
	req := httptest.NewRequest(http.MethodGet, "/drive/fetch/garbage", nil)
	res := httptest.NewRecorder()
	a.routes().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("garbage token = %d, want 403", res.Code)
	}
}

func TestSignVerifyDriveFetchTokenRoundTrip(t *testing.T) {
	secret := []byte("0123456789abcdef")
	exp := time.Now().Add(5 * time.Minute)
	token := signDriveFetchToken(secret, "wolf", "file123", exp)
	project, fileID, ok := verifyDriveFetchToken(secret, token)
	if !ok || project != "wolf" || fileID != "file123" {
		t.Fatalf("verify = (%q, %q, %v)", project, fileID, ok)
	}
}

func TestVerifyDriveFetchTokenWrongSecretFails(t *testing.T) {
	exp := time.Now().Add(5 * time.Minute)
	token := signDriveFetchToken([]byte("0123456789abcdef"), "wolf", "file123", exp)
	if _, _, ok := verifyDriveFetchToken([]byte("fedcba9876543210"), token); ok {
		t.Fatal("verified under the wrong secret")
	}
}
