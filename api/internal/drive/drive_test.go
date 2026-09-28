package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeGoogle serves both the OAuth2 token endpoint and the Drive v3 REST endpoint, so New's
// TokenSource and the drive.Service both point at it. tokenErr, when set, makes the token
// endpoint fail with that OAuth error code (e.g. "invalid_grant").
type fakeGoogle struct {
	srv      *httptest.Server
	tokenErr string

	// lastFilesQuery is the raw "q" query parameter of the last files.list call, so tests can
	// assert on escaping.
	lastFilesQuery url.Values
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	f := &fakeGoogle{}
	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if f.tokenErr != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":             f.tokenErr,
				"error_description": "Token has been expired or revoked.",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		f.lastFilesQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		pageToken := r.URL.Query().Get("pageToken")
		var files []map[string]any
		next := ""
		if pageToken == "" {
			files = []map[string]any{
				{"id": "f1", "name": "one", "mimeType": "text/plain", "modifiedTime": "2024-01-01T00:00:00Z", "parents": []string{"root"}},
			}
			next = "page2"
		} else {
			files = []map[string]any{
				{"id": "f2", "name": "two", "mimeType": "text/plain", "modifiedTime": "2024-01-02T00:00:00Z", "parents": []string{"root"}},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files, "nextPageToken": next})
	})

	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/files/")
		id = strings.TrimSuffix(id, "/export")
		alt := r.URL.Query().Get("alt")
		exportMime := r.URL.Query().Get("mimeType")

		switch id {
		case "missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"File not found: missing."}}`))
			return
		case "doc1":
			if strings.HasSuffix(r.URL.Path, "/export") {
				if exportMime != "text/markdown" {
					http.Error(w, "unexpected export mime "+exportMime, http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/markdown")
				_, _ = w.Write([]byte("# hello"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "doc1", "name": "Doc One", "mimeType": "application/vnd.google-apps.document"})
			return
		case "sheet1":
			if strings.HasSuffix(r.URL.Path, "/export") {
				if exportMime != "text/csv" {
					http.Error(w, "unexpected export mime "+exportMime, http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/csv")
				_, _ = w.Write([]byte("a,b\n1,2"))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sheet1", "name": "Sheet One", "mimeType": "application/vnd.google-apps.spreadsheet"})
			return
		case "slides1":
			if strings.HasSuffix(r.URL.Path, "/export") {
				if exportMime != "text/plain" {
					http.Error(w, "unexpected export mime "+exportMime, http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("slide text"))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "slides1", "name": "Slides One", "mimeType": "application/vnd.google-apps.presentation"})
			return
		case "text1":
			if alt == "media" {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("plain bytes"))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "text1", "name": "notes.txt", "mimeType": "text/plain"})
			return
		case "pdf1":
			if alt == "media" {
				w.Header().Set("Content-Type", "application/pdf")
				_, _ = w.Write([]byte("%PDF-fake"))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pdf1", "name": "report.pdf", "mimeType": "application/pdf"})
			return
		}
		http.Error(w, "unknown file "+id, http.StatusNotFound)
	})

	mux.HandleFunc("/about", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"emailAddress": "kai@example.com"}})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) client(t *testing.T) *Client {
	t.Helper()
	c, err := New(context.Background(), "client-id", "client-secret", "refresh-token",
		WithTokenEndpoint(f.srv.URL+"/token"),
		WithAPIEndpoint(f.srv.URL),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestSearchEscapesQuery(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	files, err := c.Search(context.Background(), `budget's "plan"`, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2 (limit=10, both pages of the fake)", len(files))
	}
	q := f.lastFilesQuery.Get("q")
	if !strings.Contains(q, `budget\'s \"plan\"`) {
		t.Errorf("q = %q, want escaped quotes", q)
	}
	if !strings.Contains(q, "fullText contains") || !strings.Contains(q, "trashed = false") {
		t.Errorf("q = %q, want fullText contains ... and trashed = false", q)
	}
	if f.lastFilesQuery.Get("corpora") != "allDrives" {
		t.Errorf("corpora = %q, want allDrives", f.lastFilesQuery.Get("corpora"))
	}
	if f.lastFilesQuery.Get("supportsAllDrives") != "true" {
		t.Errorf("supportsAllDrives = %q, want true", f.lastFilesQuery.Get("supportsAllDrives"))
	}
	if f.lastFilesQuery.Get("includeItemsFromAllDrives") != "true" {
		t.Errorf("includeItemsFromAllDrives = %q, want true", f.lastFilesQuery.Get("includeItemsFromAllDrives"))
	}
}

func TestSearchRespectsLimitAcrossPages(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	files, err := c.Search(context.Background(), "q", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1 (limit stops after first page)", len(files))
	}
	if files[0].ID != "f1" {
		t.Errorf("id = %q, want f1", files[0].ID)
	}
}

func TestListBuildsParentQuery(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	if _, _, err := c.List(context.Background(), "folder1", ""); err != nil {
		t.Fatalf("List: %v", err)
	}
	q := f.lastFilesQuery.Get("q")
	if q != `'folder1' in parents and trashed = false` {
		t.Errorf("q = %q", q)
	}
}

func TestListEmptyFolderIsRootOrShared(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	if _, _, err := c.List(context.Background(), "", "page2"); err != nil {
		t.Fatalf("List: %v", err)
	}
	q := f.lastFilesQuery.Get("q")
	if q != `('root' in parents or sharedWithMe) and trashed = false` {
		t.Errorf("q = %q", q)
	}
	if f.lastFilesQuery.Get("pageToken") != "page2" {
		t.Errorf("pageToken = %q, want page2", f.lastFilesQuery.Get("pageToken"))
	}
}

func TestReadExportsGoogleDocAsMarkdown(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	name, mime, text, err := c.Read(context.Background(), "doc1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if name != "Doc One" || mime != "text/markdown" || text != "# hello" {
		t.Errorf("got (%q, %q, %q)", name, mime, text)
	}
}

func TestReadExportsSheetAsCSV(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	_, mime, text, err := c.Read(context.Background(), "sheet1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mime != "text/csv" || text != "a,b\n1,2" {
		t.Errorf("got (%q, %q)", mime, text)
	}
}

func TestReadExportsSlidesAsPlainText(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	_, mime, text, err := c.Read(context.Background(), "slides1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mime != "text/plain" || text != "slide text" {
		t.Errorf("got (%q, %q)", mime, text)
	}
}

func TestReadReturnsPlainTextFileBytes(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	name, mime, text, err := c.Read(context.Background(), "text1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if name != "notes.txt" || mime != "text/plain" || text != "plain bytes" {
		t.Errorf("got (%q, %q, %q)", name, mime, text)
	}
}

func TestReadRejectsUnreadableMimeType(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	_, _, _, err := c.Read(context.Background(), "pdf1")
	if err == nil {
		t.Fatal("want error for application/pdf")
	}
	if !strings.Contains(err.Error(), "drive_fetch") {
		t.Errorf("error = %q, want it to mention drive_fetch", err.Error())
	}
}

func TestDownloadStreamsBytes(t *testing.T) {
	f := newFakeGoogle(t)
	c := f.client(t)

	rc, name, mime, err := c.Download(context.Background(), "pdf1")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if name != "report.pdf" || mime != "application/pdf" || string(data) != "%PDF-fake" {
		t.Errorf("got (%q, %q, %q)", name, mime, string(data))
	}
}

func TestRevokedTokenErrorMessage(t *testing.T) {
	f := newFakeGoogle(t)
	f.tokenErr = "invalid_grant"
	c := f.client(t)

	_, err := c.Search(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "revoked or expired") || !strings.Contains(err.Error(), "scripts/drive-token") {
		t.Errorf("error = %q, want the revoked/expired guidance", err.Error())
	}
}

func TestTokenVar(t *testing.T) {
	for name, want := range map[string]string{
		"enc":        "BOB_DRIVE_TOKEN_ENC",
		"marketing":  "BOB_DRIVE_TOKEN_MARKETING",
		"agent-wolf": "BOB_DRIVE_TOKEN_AGENT_WOLF",
	} {
		if got := TokenVar(name); got != want {
			t.Errorf("TokenVar(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestReadRevokedToken(t *testing.T) {
	f := newFakeGoogle(t)
	f.tokenErr = "invalid_grant"
	c := f.client(t)

	_, _, _, err := c.Read(context.Background(), "doc1")
	if err == nil || !strings.Contains(err.Error(), "revoked or expired") {
		t.Errorf("error = %v, want the revoked/expired guidance", err)
	}
}
