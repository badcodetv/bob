package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/store"
)

func TestOrigin(t *testing.T) {
	for host, want := range map[string]string{
		"bob.box.badcode.tv": "http://bob.box.badcode.tv", "localhost:8080": "http://localhost:8080", "[::1]:8090": "http://[::1]:8090",
		"evil.com; script-src *": "'self'", "": "'self'", "a b": "'self'",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		if got := origin(r); got != want {
			t.Errorf("origin(%q) = %q, want %q", host, got, want)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Host, r.Header["X-Forwarded-Proto"] = "127.0.0.1:8090", []string{"https"}
	r.Header.Set("X-Forwarded-Host", "bob.box.badcode.tv, inner")
	if got := origin(r); got != "https://bob.box.badcode.tv" {
		t.Errorf("behind a TLS proxy: %q", got)
	}
}

func TestSafeFilePath(t *testing.T) {
	for path, want := range map[string]bool{
		"": true, "/": true, "/site/index.html": true, "/site/gold%20chart.svg": true, "/..notes.md": true,
		"/..": false, "/site/../x": false, "/%2e%2e/x": false, "/%2E%2E/x": false, "/..%2fx": false, "/a%2f..%2fb": false,
		"/..%5cx": false, "/.git/config": false, "/.GIT": false, "/%2egit/config": false, "/x%00.txt": false,
		"/%zz": false, "x": false,
	} {
		if got := safeFilePath(path); got != want {
			t.Errorf("safeFilePath(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestFileViewerHeaders checks the proxy passes the runtime's answer through with the sandbox
// headers on every kind of response, and refuses escapes before reaching the runtime.
func TestFileViewerHeaders(t *testing.T) {
	st := testStore(t)
	if _, err := st.CreateProject(t.Context(), store.Project{Name: "wolf"}); err != nil {
		t.Fatal(err)
	}
	var asked []string
	rt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.EscapedPath())
		switch r.URL.EscapedPath() {
		case "/health":
		case "/files/site/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(`<script>fetch('/api/projects')</script>`))
		case "/files/site/chart%20one.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		case "/files/data.json", "/files/site":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"entries":[]}`))
		default:
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}))
	defer rt.Close()
	people := access.Map{"tester@example.com": {"wolf"}}
	a := &app{
		auth: &auth.Auth{Secret: []byte("0123456789abcdef"), Allowed: people.Allowed}, access: people,
		store: st, broker: broker.New(), runtime: fakeContainers{rt.URL}, turns: map[string]context.CancelFunc{},
	}
	a.projectOf = a.storeProjectOf
	rec := httptest.NewRecorder()
	a.auth.SetSession(rec, auth.User{Email: "tester@example.com"})
	cookie := rec.Result().Cookies()[0]

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		a.routes().ServeHTTP(rec, req)
		return rec
	}
	for path, want := range map[string]struct {
		status int
		ctype  string
	}{
		"/api/projects/wolf/files/site/index.html":      {200, "text/html; charset=utf-8"},
		"/api/projects/wolf/files/site/chart%20one.svg": {200, "image/svg+xml"},
		"/api/projects/wolf/files/data.json":            {200, "application/json"},
		"/api/projects/wolf/files/site":                 {200, "application/json"},
		"/api/projects/wolf/files/missing.html":         {404, ""},
		"/api/projects/wolf/files/%2e%2e/secret":        {404, ""},
		"/api/projects/wolf/files/.git/config":          {404, ""},
	} {
		res := get(path)
		if res.Code != want.status || (want.ctype != "" && res.Header().Get("Content-Type") != want.ctype) {
			t.Errorf("%s: %d %q, want %d %q", path, res.Code, res.Header().Get("Content-Type"), want.status, want.ctype)
		}
		for k, v := range fileHeaders {
			if v = strings.ReplaceAll(v, "ORIGIN", "http://example.com"); res.Header().Get(k) != v {
				got := res.Header().Get(k)
				t.Errorf("%s: %s = %q, want %q", path, k, got, v)
			}
		}
	}
	if !strings.HasPrefix(fileHeaders["Content-Security-Policy"], "sandbox; default-src 'none';") {
		t.Error("the policy must start with a bare sandbox (no allow-scripts, no allow-same-origin)")
	}
	for _, p := range asked {
		if strings.Contains(p, "%2e") || strings.Contains(p, ".git") {
			t.Errorf("an escape reached the runtime: %s", p)
		}
	}
	if res := get("/api/projects/other/files/site/index.html"); res.Code != 404 {
		t.Errorf("another project's file: %d", res.Code)
	}

	// A viewer link reads the same files without the cookie, and only that project's.
	req := httptest.NewRequest("POST", "/api/projects/wolf/view", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	a.routes().ServeHTTP(rec, req)
	var link struct{ Base string }
	json.NewDecoder(rec.Body).Decode(&link)
	anon := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.routes().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if res := anon(link.Base + "site/index.html"); res.Code != 200 || !strings.HasPrefix(res.Header().Get("Content-Security-Policy"), "sandbox;") {
		t.Errorf("viewer link: %d %v", res.Code, res.Header())
	}
	for _, path := range []string{link.Base + "%2e%2e/x", link.Base + ".git/config", "/api/view/forged/site/index.html",
		"/api/view/" + a.auth.Token("view", "wolf|stranger@example.com", time.Hour) + "/site/index.html",
		"/api/view/" + a.auth.Token("view", "other|tester@example.com", time.Hour) + "/site/index.html",
		"/api/view/" + a.auth.Token("view", "wolf|tester@example.com", -time.Second) + "/site/index.html"} {
		if res := anon(path); res.Code != 404 || res.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: %d, want 404 with the policy", path, res.Code)
		}
	}
	if res := get("/api/projects/wolf/files/site/index.html"); res.Code != 200 {
		t.Errorf("still works with the cookie: %d", res.Code)
	}
}
