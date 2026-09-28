// Package drive is Bob's read-only client for a project's Google Drive, built once per project
// from that project's own OAuth refresh token (BOB_DRIVE_TOKEN_<NAME> in the API's environment).
// It never writes: the only scope it ever requests is drive.readonly (scripts/drive-token mints
// the token with that scope; nothing here asks for more).
package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// TokenVar names the variable holding a project's Drive refresh token
// (BOB_DRIVE_TOKEN_<NAME>, minted once by scripts/drive-token). A project with no such variable
// set has no Drive client and none of the drive_* MCP tools. See runtime.TokenVar for the same
// upper-casing rule.
func TokenVar(project string) string {
	return "BOB_DRIVE_TOKEN_" + strings.ToUpper(strings.ReplaceAll(project, "-", "_"))
}

// File is the subset of a Drive file's metadata Search and List return.
type File struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	MimeType   string   `json:"mime_type"`
	ModifiedAt string   `json:"modified_at"`
	Parents    []string `json:"parents,omitempty"`
}

// Client is one project's read-only view of its Google Drive.
type Client struct {
	svc *drive.Service
}

// config holds the endpoint overrides tests use in place of Google's real ones. Options set it;
// callers of New never see it.
type config struct {
	tokenEndpoint string
	apiEndpoint   string
}

// Option configures a Client built by New. WithTokenEndpoint and WithAPIEndpoint exist so tests
// can point a Client at an httptest fake instead of Google; production code never needs them.
type Option func(*config)

// WithTokenEndpoint overrides the OAuth2 token endpoint used to exchange the refresh token for an
// access token. Tests only.
func WithTokenEndpoint(url string) Option {
	return func(c *config) { c.tokenEndpoint = url }
}

// WithAPIEndpoint overrides the base URL of the Drive REST API. Tests only.
func WithAPIEndpoint(url string) Option {
	return func(c *config) { c.apiEndpoint = url }
}

// New builds a project's Drive client from the OAuth client that minted refreshToken
// (scripts/drive-token) and that refresh token itself. It does not make a network call: a bad
// client id/secret or a revoked token only surfaces on the first Search/List/Read/Download call
// (see wrapErr).
func New(ctx context.Context, clientID, clientSecret, refreshToken string, opts ...Option) (*Client, error) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	oauthCfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{drive.DriveReadonlyScope},
	}
	if cfg.tokenEndpoint != "" {
		oauthCfg.Endpoint = oauth2.Endpoint{TokenURL: cfg.tokenEndpoint}
	}
	httpClient := oauthCfg.Client(ctx, &oauth2.Token{RefreshToken: refreshToken})

	svcOpts := []option.ClientOption{option.WithHTTPClient(httpClient)}
	if cfg.apiEndpoint != "" {
		svcOpts = append(svcOpts, option.WithEndpoint(cfg.apiEndpoint))
	}
	svc, err := drive.NewService(ctx, svcOpts...)
	if err != nil {
		return nil, fmt.Errorf("drive: %w", err)
	}
	return &Client{svc: svc}, nil
}

// escapeQuery escapes a string for use inside a single-quoted Drive query term, per
// https://developers.google.com/drive/api/guides/search-files: backslash and single quote are
// backslash-escaped. Drive's `contains`/`=` terms also accept an escaped double quote inside the
// single-quoted term, which callers may pass through unescaped user text containing quotes.
func escapeQuery(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// Search finds files whose full text contains q, most recently modified first, stopping once
// limit files have been collected (limit <= 0 means the caller wants none).
func (c *Client) Search(ctx context.Context, q string, limit int) ([]File, error) {
	query := fmt.Sprintf("fullText contains '%s' and trashed = false", escapeQuery(q))
	return c.listFiles(ctx, query, "", limit)
}

// List returns one page of the children of folderID (or, when folderID is empty, of My Drive's
// root and everything shared with the caller), and the token for the next page ("" if this was
// the last).
func (c *Client) List(ctx context.Context, folderID, pageToken string) ([]File, string, error) {
	var query string
	if folderID == "" {
		query = "('root' in parents or sharedWithMe) and trashed = false"
	} else {
		query = fmt.Sprintf("'%s' in parents and trashed = false", escapeQuery(folderID))
	}
	files, next, err := c.listFilesPage(ctx, query, pageToken)
	if err != nil {
		return nil, "", wrapErr(err)
	}
	return files, next, nil
}

// listFiles collects up to limit files across as many pages as needed (used by Search, where the
// caller does not manage paging itself).
func (c *Client) listFiles(ctx context.Context, query, pageToken string, limit int) ([]File, error) {
	var out []File
	for {
		if limit > 0 && len(out) >= limit {
			out = out[:limit]
			break
		}
		files, next, err := c.listFilesPage(ctx, query, pageToken)
		if err != nil {
			return nil, wrapErr(err)
		}
		out = append(out, files...)
		if next == "" || (limit > 0 && len(out) >= limit) {
			if limit > 0 && len(out) > limit {
				out = out[:limit]
			}
			break
		}
		pageToken = next
	}
	return out, nil
}

func (c *Client) listFilesPage(ctx context.Context, query, pageToken string) ([]File, string, error) {
	call := c.svc.Files.List().
		Context(ctx).
		Q(query).
		Corpora("allDrives").
		SupportsAllDrives(true).
		IncludeItemsFromAllDrives(true).
		Fields("nextPageToken, files(id, name, mimeType, modifiedTime, parents)")
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	list, err := call.Do()
	if err != nil {
		return nil, "", wrapErr(err)
	}
	files := make([]File, 0, len(list.Files))
	for _, f := range list.Files {
		files = append(files, File{ID: f.Id, Name: f.Name, MimeType: f.MimeType, ModifiedAt: f.ModifiedTime, Parents: f.Parents})
	}
	return files, list.NextPageToken, nil
}

// exportMimeFor maps a Google-native file's mimeType to what drive_read exports it as. ok is
// false when the file must instead be read as plain bytes (text/* or application/json, handled by
// the caller) or fetched with Download.
func exportMimeFor(mimeType string) (exportMime string, ok bool) {
	switch mimeType {
	case "application/vnd.google-apps.document":
		return "text/markdown", true
	case "application/vnd.google-apps.spreadsheet":
		return "text/csv", true
	case "application/vnd.google-apps.presentation":
		return "text/plain", true
	}
	return "", false
}

// Read returns a file's text. Google Docs export as markdown, Sheets as CSV (first sheet only),
// Slides as plain text; a text/* or application/json file is returned as-is; anything else is an
// error telling the caller to use Download (drive_fetch) instead.
func (c *Client) Read(ctx context.Context, fileID string) (name, mime, text string, err error) {
	meta, err := c.svc.Files.Get(fileID).Context(ctx).
		SupportsAllDrives(true).
		Fields("id, name, mimeType").
		Do()
	if err != nil {
		return "", "", "", wrapErr(err)
	}

	if exportMime, ok := exportMimeFor(meta.MimeType); ok {
		resp, err := c.svc.Files.Export(fileID, exportMime).Context(ctx).Download()
		if err != nil {
			return "", "", "", wrapErr(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", "", "", wrapErr(err)
		}
		return meta.Name, exportMime, string(body), nil
	}

	if strings.HasPrefix(meta.MimeType, "text/") || meta.MimeType == "application/json" {
		resp, err := c.svc.Files.Get(fileID).Context(ctx).SupportsAllDrives(true).Download()
		if err != nil {
			return "", "", "", wrapErr(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", "", "", wrapErr(err)
		}
		return meta.Name, meta.MimeType, string(body), nil
	}

	return "", "", "", fmt.Errorf("%s is a %s file: drive_read can't show it as text, use drive_fetch to download it instead", meta.Name, meta.MimeType)
}

// Download streams a file's raw bytes (not exported), for drive_fetch. The caller must close the
// returned ReadCloser.
func (c *Client) Download(ctx context.Context, fileID string) (rc io.ReadCloser, name, mime string, err error) {
	meta, err := c.svc.Files.Get(fileID).Context(ctx).
		SupportsAllDrives(true).
		Fields("id, name, mimeType").
		Do()
	if err != nil {
		return nil, "", "", wrapErr(err)
	}
	resp, err := c.svc.Files.Get(fileID).Context(ctx).SupportsAllDrives(true).Download()
	if err != nil {
		return nil, "", "", wrapErr(err)
	}
	return resp.Body, meta.Name, meta.MimeType, nil
}

// wrapErr turns a revoked or expired refresh token (an OAuth2 "invalid_grant") into a message an
// agent (or Kai) can act on. Every other error passes through unchanged.
func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant" {
		return fmt.Errorf("the Drive connection for this project was revoked or expired; re-run scripts/drive-token and update BOB_DRIVE_TOKEN_<NAME>")
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) && apiErr.Code == http.StatusUnauthorized {
		return fmt.Errorf("the Drive connection for this project was revoked or expired; re-run scripts/drive-token and update BOB_DRIVE_TOKEN_<NAME>")
	}
	return err
}
