// Package auth is Google sign-in for Bob: the browser gets an ID token from Google Identity
// Services, Bob checks it with Google once, and issues its own signed session cookie.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const cookieName = "bob_session"
const sessionTTL = 7 * 24 * time.Hour

type Auth struct {
	ClientID string
	Secret   []byte
	Allowed  func(email string) bool // may this lower-case email sign in?
	// TokenInfoURL is Google's ID-token check; overridable in tests.
	TokenInfoURL string
}

// User is a signed-in person.
type User struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// VerifyGoogle checks an ID token with Google and returns the signed-in person.
func (a *Auth) VerifyGoogle(ctx context.Context, idToken string) (User, error) {
	base := a.TokenInfoURL
	if base == "" {
		base = "https://oauth2.googleapis.com/tokeninfo"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"?id_token="+url.QueryEscape(idToken), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return User{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return User{}, errors.New("google rejected the sign-in token")
	}
	var info struct {
		Aud           string `json:"aud"`
		Email         string `json:"email"`
		EmailVerified string `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return User{}, err
	}
	email := strings.ToLower(info.Email)
	switch {
	case info.Aud != a.ClientID:
		return User{}, errors.New("sign-in token was issued for a different app")
	case info.EmailVerified != "true":
		return User{}, errors.New("google account email is not verified")
	case !a.Allowed(email):
		return User{}, fmt.Errorf("%s is not allowed to use this Bob", email)
	}
	return User{Email: email, Name: info.Name}, nil
}

// SetSession writes the session cookie for a person.
func (a *Auth) SetSession(w http.ResponseWriter, u User) {
	value := a.sign(u.Email + "|" + strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10) + "|" + u.Name)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
}

func (a *Auth) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
}

// Email returns the signed-in person's email for a request, or "".
func (a *Auth) Email(r *http.Request) string { return a.User(r).Email }

// User returns the signed-in person for a request; the zero User when nobody is.
func (a *Auth) User(r *http.Request) User {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return User{}
	}
	payload, ok := a.verify(c.Value)
	if !ok {
		return User{}
	}
	// email|expiry|name — the name may itself contain "|"; cookies from before names have none.
	parts := strings.SplitN(payload, "|", 3)
	if len(parts) < 2 {
		return User{}
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expiry || !a.Allowed(parts[0]) {
		return User{}
	}
	u := User{Email: parts[0]}
	if len(parts) == 3 {
		u.Name = parts[2]
	}
	return u
}

func (a *Auth) sign(payload string) string {
	mac := hmac.New(sha256.New, a.Secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Auth) verify(value string) (string, bool) {
	p, s, ok := strings.Cut(value, ".")
	if !ok {
		return "", false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	sig, err2 := base64.RawURLEncoding.DecodeString(s)
	if err1 != nil || err2 != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, a.Secret)
	mac.Write(payload)
	return string(payload), hmac.Equal(sig, mac.Sum(nil))
}
