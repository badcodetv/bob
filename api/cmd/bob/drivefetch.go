package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// signDriveFetchToken mints a drive_fetch URL's token: base64url(project|fileID|expUnix) + "." +
// hex(HMAC-SHA256(secret, "drive-fetch\x00" + payload)). It carries its own expiry rather than
// being looked up, so GET /drive/fetch/{token} needs no session or database lookup — just the
// secret every Bob instance already holds (BOB_SESSION_SECRET).
func signDriveFetchToken(secret []byte, project, fileID string, exp time.Time) string {
	payload := driveFetchPayload(project, fileID, exp)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + driveFetchSig(secret, payload)
}

// verifyDriveFetchToken checks a drive_fetch token's HMAC (constant time) and expiry, returning
// the project and file it names. A tampered or expired token is reported the same way (ok=false):
// GET /drive/fetch/{token} turns either into a 403, never revealing which.
func verifyDriveFetchToken(secret []byte, token string) (project, fileID string, ok bool) {
	encPayload, sig, found := strings.Cut(token, ".")
	if !found || encPayload == "" || sig == "" {
		return "", "", false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(encPayload)
	if err != nil {
		return "", "", false
	}
	payload := string(payloadBytes)
	if !hmac.Equal([]byte(sig), []byte(driveFetchSig(secret, payload))) {
		return "", "", false
	}
	parts := strings.SplitN(payload, "|", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", "", false
	}
	if time.Now().Unix() > exp {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func driveFetchPayload(project, fileID string, exp time.Time) string {
	return project + "|" + fileID + "|" + strconv.FormatInt(exp.Unix(), 10)
}

func driveFetchSig(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("drive-fetch\x00" + payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// driveFetch serves GET /drive/fetch/{token}, outside requireLogin: the token itself, signed and
// expiring in 10 minutes (tools_drive.go), is the credential — curl has no cookie to send. A bad
// or expired token, or a project that no longer has a Drive client, is 403 either way.
func (a *app) driveFetch(w http.ResponseWriter, r *http.Request) {
	project, fileID, ok := verifyDriveFetchToken(a.auth.Secret, r.PathValue("token"))
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cl, ok := a.drive[project]
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rc, name, mime, err := cl.Download(r.Context(), fileID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer rc.Close()
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	_, _ = io.Copy(w, rc)
}
