package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionCookieRoundTrip(t *testing.T) {
	a := &Auth{Secret: []byte("s"), Allowed: map[string]bool{"kai@example.com": true}}
	rec := httptest.NewRecorder()
	a.SetSession(rec, "kai@example.com")
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])
	if got := a.Email(req); got != "kai@example.com" {
		t.Fatalf("Email = %q", got)
	}

	other := &Auth{Secret: []byte("different"), Allowed: a.Allowed}
	if got := other.Email(req); got != "" {
		t.Fatalf("cookie signed with another secret accepted: %q", got)
	}
	removed := &Auth{Secret: a.Secret, Allowed: map[string]bool{}}
	if got := removed.Email(req); got != "" {
		t.Fatalf("cookie for a no-longer-allowed email accepted: %q", got)
	}
}

func TestVerifyGoogle(t *testing.T) {
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aud, email := "client", "Kai@Example.com"
		switch r.URL.Query().Get("id_token") {
		case "bad":
			http.Error(w, "invalid", 400)
			return
		case "other-app":
			aud = "someone-else"
		case "stranger":
			email = "stranger@example.com"
		}
		fmt.Fprintf(w, `{"aud":%q,"email":%q,"email_verified":"true"}`, aud, email)
	}))
	defer google.Close()
	a := &Auth{ClientID: "client", Allowed: map[string]bool{"kai@example.com": true}, TokenInfoURL: google.URL}

	if email, err := a.VerifyGoogle(t.Context(), "good"); err != nil || email != "kai@example.com" {
		t.Fatalf("good token: %q, %v", email, err)
	}
	for token, want := range map[string]string{"bad": "rejected", "other-app": "different app", "stranger": "not allowed"} {
		if _, err := a.VerifyGoogle(t.Context(), token); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to mention %q", token, err, want)
		}
	}
}
