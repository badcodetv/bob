package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionCookieRoundTrip(t *testing.T) {
	a := &Auth{Secret: []byte("s"), Allowed: func(e string) bool { return e == "kai@example.com" }}
	rec := httptest.NewRecorder()
	a.SetSession(rec, User{Email: "kai@example.com", Name: "Kai | Davenport"})
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])
	if got := a.User(req); got != (User{Email: "kai@example.com", Name: "Kai | Davenport"}) {
		t.Fatalf("User = %+v", got)
	}

	other := &Auth{Secret: []byte("different"), Allowed: a.Allowed}
	if got := other.Email(req); got != "" {
		t.Fatalf("cookie signed with another secret accepted: %q", got)
	}
	removed := &Auth{Secret: a.Secret, Allowed: func(string) bool { return false }}
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
		fmt.Fprintf(w, `{"aud":%q,"email":%q,"email_verified":"true","name":"Kai"}`, aud, email)
	}))
	defer google.Close()
	a := &Auth{ClientID: "client", Allowed: func(e string) bool { return e == "kai@example.com" }, TokenInfoURL: google.URL}

	if u, err := a.VerifyGoogle(t.Context(), "good"); err != nil || u != (User{Email: "kai@example.com", Name: "Kai"}) {
		t.Fatalf("good token: %+v, %v", u, err)
	}
	for token, want := range map[string]string{"bad": "rejected", "other-app": "different app", "stranger": "not allowed"} {
		if _, err := a.VerifyGoogle(t.Context(), token); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to mention %q", token, err, want)
		}
	}
}
