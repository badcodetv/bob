package main

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/badcodetv/bob/internal/runtime"
)

// fileHeaders go on every response from the file viewer, whatever the file. Agents write these
// files, and an agent can be prompt-injected, so a page must never act as the signed-in person:
// "sandbox" without allow-scripts or allow-same-origin runs no script and gives the page an opaque
// origin (no cookie, no API) even when opened directly in a tab; default-src 'none' stops it
// reaching any other host.
//
// A sandboxed page has an opaque origin, so 'self' in its policy matches nothing and its own
// images and stylesheets would be blocked; the policy names Bob's origin instead.
var fileHeaders = map[string]string{
	"Content-Security-Policy": "sandbox; default-src 'none'; img-src ORIGIN data:; style-src ORIGIN 'unsafe-inline'; font-src ORIGIN data:; frame-ancestors 'self'",
	"X-Content-Type-Options":  "nosniff",
	"Referrer-Policy":         "no-referrer",
	"Cache-Control":           "no-store",
}

var plainHost = regexp.MustCompile(`^([A-Za-z0-9.-]+|\[[0-9A-Fa-f:.]+\])(:[0-9]{1,5})?$`)

// origin is Bob's own origin as the browser sees it: X-Forwarded-Host from a proxy in front of
// Bob, else Host. When that is not a plain host it is 'self', which blocks the page's images and
// styles but nothing worse. (A client choosing these headers only changes its own response.)
func origin(r *http.Request) string {
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	if !plainHost.MatchString(host) {
		return "'self'"
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + host
}

// viewTTL is how long a viewer link works. Access is checked again on every request.
const viewTTL = 12 * time.Hour

// getFile is GET /api/projects/{p}/files/<path>, for a signed-in member.
func (a *app) getFile(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	a.serveFile(w, r, project, strings.TrimPrefix(r.URL.EscapedPath(), "/api/projects/"+project+"/files"))
}

// viewLink is POST /api/projects/{p}/view: a link prefix under which the project's files can be
// read without the session cookie, for viewing pages. A sandboxed page has an opaque origin, so
// the browser never sends Bob's cookie with its images and stylesheets; the link carries a signed
// token instead, naming the project and the person, and the files under it resolve relative
// links. It grants reading this project's files and nothing else.
func (a *app) viewLink(w http.ResponseWriter, r *http.Request) {
	token := a.auth.Token("view", r.PathValue("project")+"|"+a.auth.Email(r), viewTTL)
	reply(w, map[string]string{"base": "/api/view/" + token + "/"}, nil)
}

// viewFile is GET /api/view/{token}/<path>: a file for the person and project the token names,
// while that person may still use the project.
func (a *app) viewFile(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	payload, ok := a.auth.CheckToken("view", token)
	project, email, _ := strings.Cut(payload, "|")
	if !ok || !a.auth.Allowed(email) || !a.access.Member(email, project) {
		for k, v := range fileHeaders {
			w.Header().Set(k, strings.ReplaceAll(v, "ORIGIN", origin(r)))
		}
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	a.serveFile(w, r, project, strings.TrimPrefix(r.URL.EscapedPath(), "/api/view/"+token))
}

// serveFile proxies a file request to the project's runtime, which serves the synced checkout.
// The path is passed on still escaped; the runtime does the containment checks, and anything
// with a ".." or .git segment is refused here too.
func (a *app) serveFile(w http.ResponseWriter, r *http.Request, project, rest string) {
	for k, v := range fileHeaders {
		w.Header().Set(k, strings.ReplaceAll(v, "ORIGIN", origin(r)))
	}
	if !safeFilePath(rest) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	p, err := a.store.Project(r.Context(), project)
	if err != nil {
		reply(w, nil, err)
		return
	}
	base, err := a.runtime.Ensure(r.Context(), p)
	if err != nil {
		reply(w, nil, err)
		return
	}
	resp, err := runtime.File(r.Context(), base, rest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, k := range []string{"Content-Type", "Content-Length"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// safeFilePath checks an escaped path below /files: every segment must decode, and none may be
// "..", ".git", or hide a slash, backslash or NUL.
func safeFilePath(escaped string) bool {
	if escaped != "" && !strings.HasPrefix(escaped, "/") {
		return false
	}
	for _, raw := range strings.Split(escaped, "/") {
		seg, err := url.PathUnescape(raw)
		if err != nil || seg == ".." || strings.EqualFold(seg, ".git") || strings.ContainsAny(seg, "/\\\x00") {
			return false
		}
	}
	return true
}
