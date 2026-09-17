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

// VerifyGoogle checks an ID token with Google and returns the signed-in email.
func (a *Auth) VerifyGoogle(ctx context.Context, idToken string) (string, error) {
	base := a.TokenInfoURL
	if base == "" {
		base = "https://oauth2.googleapis.com/tokeninfo"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"?id_token="+url.QueryEscape(idToken), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("google rejected the sign-in token")
	}
	var info struct {
		Aud           string `json:"aud"`
		Email         string `json:"email"`
		EmailVerified string `json:"email_verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	email := strings.ToLower(info.Email)
	switch {
	case info.Aud != a.ClientID:
		return "", errors.New("sign-in token was issued for a different app")
	case info.EmailVerified != "true":
		return "", errors.New("google account email is not verified")
	case !a.Allowed(email):
		return "", fmt.Errorf("%s is not allowed to use this Bob", email)
	}
	return email, nil
}

// SetSession writes the session cookie for email.
func (a *Auth) SetSession(w http.ResponseWriter, email string) {
	value := a.sign(email + "|" + strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10))
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
}

func (a *Auth) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
}

// Email returns the signed-in user for a request, or "".
func (a *Auth) Email(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	payload, ok := a.verify(c.Value)
	if !ok {
		return ""
	}
	email, exp, found := strings.Cut(payload, "|")
	expiry, err := strconv.ParseInt(exp, 10, 64)
	if !found || err != nil || time.Now().Unix() > expiry || !a.Allowed(email) {
		return ""
	}
	return email
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
