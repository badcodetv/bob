package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
)

// TestRouteAccess checks every guarded route's policy: (who, route) → status. Handlers are
// stubbed to 200, so a 200 means the guard let the request through.
func TestRouteAccess(t *testing.T) {
	people := access.Map{"admin@example.com": {"*"}, "tester@example.com": {"wolf"}}
	a := &app{
		auth:   &auth.Auth{Secret: []byte("0123456789abcdef"), Allowed: people.Allowed},
		access: people,
		projectOf: func(_ context.Context, kind, id string) string {
			return map[string]string{"session:wolf-chat": "wolf", "session:enc-chat": "enc", "schedule:wolf-daily": "wolf", "schedule:enc-daily": "enc"}[kind+":"+id]
		},
	}
	h := a.mux(func(w http.ResponseWriter, r *http.Request) {})

	cookie := func(email string) *http.Cookie {
		rec := httptest.NewRecorder()
		a.auth.SetSession(rec, auth.User{Email: email})
		return rec.Result().Cookies()[0]
	}
	const (
		admin   = "admin@example.com"
		tester  = "tester@example.com"
		nobody  = ""
		removed = "removed@example.com" // a valid cookie for someone no longer in the map
	)
	cases := []struct {
		who, method, path string
		want              int
	}{
		{nobody, "GET", "/api/projects", 401},
		{removed, "GET", "/api/projects", 401},
		{tester, "GET", "/api/projects", 200},
		{tester, "POST", "/api/projects", 403},
		{admin, "POST", "/api/projects", 200},

		{tester, "GET", "/api/projects/wolf", 200},
		{tester, "GET", "/api/projects/enc", 404},
		{tester, "GET", "/api/projects/missing", 404},
		{admin, "GET", "/api/projects/enc", 200},
		{tester, "PATCH", "/api/projects/wolf", 403},
		{tester, "PATCH", "/api/projects/enc", 404},
		{admin, "PATCH", "/api/projects/wolf", 200},
		{tester, "DELETE", "/api/projects/wolf", 403},
		{admin, "DELETE", "/api/projects/enc", 200},
		{tester, "POST", "/api/projects/wolf/restart", 403},
		{tester, "POST", "/api/projects/wolf/sync", 200},
		{tester, "POST", "/api/projects/enc/sync", 404},
		{tester, "GET", "/api/projects/wolf/workers", 200},
		{tester, "GET", "/api/projects/enc/workers", 404},
		{tester, "GET", "/api/projects/wolf/sessions", 200},
		{tester, "POST", "/api/projects/wolf/sessions", 200},
		{tester, "POST", "/api/projects/enc/sessions", 404},

		{tester, "GET", "/api/sessions/wolf-chat", 200},
		{tester, "GET", "/api/sessions/enc-chat", 404},
		{tester, "GET", "/api/sessions/no-such-chat", 404},
		{admin, "GET", "/api/sessions/enc-chat", 200},
		{admin, "GET", "/api/sessions/no-such-chat", 404},
		{tester, "PATCH", "/api/sessions/wolf-chat", 200},
		{tester, "PATCH", "/api/sessions/enc-chat", 404},
		{tester, "DELETE", "/api/sessions/wolf-chat", 200},
		{tester, "DELETE", "/api/sessions/enc-chat", 404},
		{tester, "GET", "/api/sessions/enc-chat/events", 404},
		{tester, "GET", "/api/sessions/enc-chat/stream", 404},
		{tester, "POST", "/api/sessions/wolf-chat/messages", 200},
		{tester, "POST", "/api/sessions/enc-chat/messages", 404},
		{tester, "POST", "/api/sessions/enc-chat/interrupt", 404},

		{tester, "GET", "/api/projects/wolf/schedules", 200},
		{tester, "GET", "/api/projects/enc/schedules", 404},
		{tester, "POST", "/api/projects/wolf/schedules", 403},
		{admin, "POST", "/api/projects/wolf/schedules", 200},
		{tester, "PATCH", "/api/schedules/wolf-daily", 403},
		{tester, "PATCH", "/api/schedules/enc-daily", 404},
		{admin, "PATCH", "/api/schedules/enc-daily", 200},
		{tester, "DELETE", "/api/schedules/wolf-daily", 403},
		{admin, "DELETE", "/api/schedules/no-such", 404},
		{tester, "POST", "/api/schedules/wolf-daily/run", 200},
		{tester, "POST", "/api/schedules/enc-daily/run", 404},
		{tester, "GET", "/api/schedules/wolf-daily/runs", 200},
		{tester, "GET", "/api/schedules/enc-daily/runs", 404},
		{tester, "GET", "/api/projects/wolf/files/site/index.html", 200},
		{tester, "GET", "/api/projects/enc/files/site/index.html", 404},
		{tester, "POST", "/api/projects/wolf/view", 200},
		{tester, "POST", "/api/projects/enc/view", 404},
		{nobody, "POST", "/api/projects/wolf/view", 401},
		{tester, "GET", "/api/projects/wolf/secrets", 403},
		{tester, "GET", "/api/projects/enc/secrets", 404},
		{admin, "GET", "/api/projects/enc/secrets", 200},
		{tester, "PUT", "/api/projects/wolf/secrets/FRED_API_KEY", 403},
		{admin, "PUT", "/api/projects/wolf/secrets/FRED_API_KEY", 200},
		{tester, "DELETE", "/api/projects/wolf/secrets/FRED_API_KEY", 403},
		{tester, "GET", "/api/settings", 200},
		{tester, "PATCH", "/api/settings", 403},
		{admin, "PATCH", "/api/settings", 200},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if c.who != nobody {
			req.AddCookie(cookie(c.who))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s %s = %d, want %d", c.who, c.method, c.path, rec.Code, c.want)
		}
	}
}
