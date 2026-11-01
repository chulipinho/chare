package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"chare/internal/library"
	"chare/internal/render"
)

func newTestServer(t *testing.T) (http.Handler, *library.Library) {
	t.Helper()
	return newTestServerConfig(t, Config{})
}

func newTestServerConfig(t *testing.T, cfg Config) (http.Handler, *library.Library) {
	t.Helper()
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h, err := New(lib, render.New(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h, lib
}

func writeDoc(t *testing.T, lib *library.Library, rel, content string) {
	t.Helper()
	if _, err := lib.Write(rel, []byte(content), true); err != nil {
		t.Fatalf("Write(%q): %v", rel, err)
	}
}

func makeDir(t *testing.T, lib *library.Library, rel string) {
	t.Helper()
	if _, err := lib.Mkdir(rel); err != nil {
		t.Fatalf("Mkdir(%q): %v", rel, err)
	}
}

func do(t *testing.T, h http.Handler, method, target string, body *bytes.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, body)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func postForm(t *testing.T, h http.Handler, target string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, http.MethodPost, target, bytes.NewReader([]byte(values.Encode())), "application/x-www-form-urlencoded")
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body:\n%s", rec.Code, want, rec.Body.String())
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q:\n%s", want, got)
	}
}

func TestRootRedirect(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/", nil, "")
	assertStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/browse/" {
		t.Fatalf("Location = %q, want /browse/", got)
	}

	rec = do(t, h, http.MethodGet, "/browse", nil, "")
	assertStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/browse/" {
		t.Fatalf("Location = %q, want /browse/", got)
	}
}

func TestBrowseListingEscapesPaths(t *testing.T) {
	h, lib := newTestServer(t)
	writeDoc(t, lib, "a b.md", "# A\n")

	makeDir(t, lib, "folder")

	rec := do(t, h, http.MethodGet, "/browse/", nil, "")
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	assertContains(t, body, "/read/a%20b.md")
	assertContains(t, body, "Library")
	assertContains(t, body, "Only empty folders can be deleted.")
	assertContains(t, body, `name="path" value="folder"`)
}

func TestRichReader(t *testing.T) {
	h, lib := newTestServer(t)
	writeDoc(t, lib, "doc.md", "# Title\n\n## Section\n\nHello rich reader.\n")

	rec := do(t, h, http.MethodGet, "/read/doc.md", nil, "")
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	assertContains(t, body, "<h1")
	assertContains(t, body, "Contents")
	assertContains(t, body, "Section")
	assertContains(t, body, "Hello rich reader.")
	assertContains(t, body, "/simple/doc.md")
	assertContains(t, body, "/raw/doc.md")
}

func TestSimpleReader(t *testing.T) {
	h, lib := newTestServer(t)
	writeDoc(t, lib, "simple.md", "# Simple\n\nHello simple reader.\n")

	rec := do(t, h, http.MethodGet, "/simple/simple.md", nil, "")
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	assertContains(t, body, "Hello simple reader.")
	assertContains(t, body, "Simple")
	assertContains(t, body, "display: block")
	assertContains(t, body, "overflow-x: auto")
	if strings.Contains(body, "<script") {
		t.Fatalf("simple reader contains a script tag:\n%s", body)
	}
	if strings.Contains(body, "/static/") {
		t.Fatalf("simple reader references an external stylesheet:\n%s", body)
	}
}

func TestRawDocument(t *testing.T) {
	h, lib := newTestServer(t)
	writeDoc(t, lib, "raw.md", "# Raw\n")

	rec := do(t, h, http.MethodGet, "/raw/raw.md", nil, "")
	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/markdown") {
		t.Fatalf("Content-Type = %q", got)
	}
	assertContains(t, rec.Body.String(), "# Raw")

	writeDoc(t, lib, "raw.txt", "plain text")
	rec = do(t, h, http.MethodGet, "/raw/raw.txt", nil, "")
	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestAsset(t *testing.T) {
	h, lib := newTestServer(t)
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if _, err := lib.Write("pic.png", png, false); err != nil {
		t.Fatalf("Write asset: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/assets/pic.png", nil, "")
	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Fatal("asset body differs from stored bytes")
	}
}

func TestNewDocument(t *testing.T) {
	h, lib := newTestServer(t)

	rec := postForm(t, h, "/ui/new", url.Values{
		"dir":     {""},
		"name":    {"note"},
		"content": {"# Note\n"},
	})
	assertStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/read/note.md" {
		t.Fatalf("Location = %q, want /read/note.md", got)
	}
	if _, err := lib.Stat("note.md"); err != nil {
		t.Fatalf("created document missing: %v", err)
	}
}

func TestUpload(t *testing.T) {
	h, lib := newTestServer(t)
	makeDir(t, lib, "sub")

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("dir", "sub"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("files", "upload.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("uploaded")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	rec := do(t, h, http.MethodPost, "/ui/upload", bytes.NewReader(buf.Bytes()), writer.FormDataContentType())
	assertStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/browse/sub" {
		t.Fatalf("Location = %q, want /browse/sub", got)
	}
	if _, err := lib.Stat("sub/upload.txt"); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
}

func TestCSRFProtection(t *testing.T) {
	h, lib := newTestServer(t)

	tests := []struct {
		name       string
		folder     string
		headers    map[string]string
		wantStatus int
	}{
		{
			name:       "evil origin",
			folder:     "evil",
			headers:    map[string]string{"Origin": "https://evil.example"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "cross-site fetch metadata",
			folder:     "blocked",
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "same origin",
			folder:     "same-origin",
			headers:    map[string]string{"Origin": "http://example.com"},
			wantStatus: http.StatusSeeOther,
		},
		{
			name:       "null origin with same-origin fetch metadata",
			folder:     "null-same-origin",
			headers:    map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin"},
			wantStatus: http.StatusSeeOther,
		},
		{
			name:       "null origin without fetch metadata",
			folder:     "null-no-metadata",
			headers:    map[string]string{"Origin": "null"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "null origin with cross-site fetch metadata",
			folder:     "null-cross-site",
			headers:    map[string]string{"Origin": "null", "Sec-Fetch-Site": "cross-site"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "rewritten host with same-origin fetch metadata",
			folder:     "proxy-rewrite",
			headers:    map[string]string{"Origin": "http://proxy.example", "Sec-Fetch-Site": "same-origin"},
			wantStatus: http.StatusSeeOther,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := url.Values{"dir": {""}, "name": {test.folder}}.Encode()
			req := httptest.NewRequest(http.MethodPost, "/ui/mkdir", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for key, value := range test.headers {
				req.Header.Set(key, value)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assertStatus(t, rec, test.wantStatus)
			_, err := lib.Stat(test.folder)
			if test.wantStatus == http.StatusSeeOther {
				if err != nil {
					t.Fatalf("request did not create a folder: %v", err)
				}
			} else if err == nil {
				t.Fatal("blocked request created a folder")
			}
		})
	}
}

func TestUploadTooLarge(t *testing.T) {
	h, lib := newTestServerConfig(t, Config{MaxUpload: 4})

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("files", "large.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	rec := do(t, h, http.MethodPost, "/ui/upload", bytes.NewReader(buf.Bytes()), writer.FormDataContentType())
	assertStatus(t, rec, http.StatusRequestEntityTooLarge)
	assertContains(t, rec.Body.String(), "too large")
	if _, err := lib.Stat("large.txt"); err == nil {
		t.Fatal("oversized file was written")
	}
}

func TestUploadEnvelopeTooLarge(t *testing.T) {
	h, _ := newTestServerConfig(t, Config{MaxUpload: 4})

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("files", "huge.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), (1<<20)+64)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	rec := do(t, h, http.MethodPost, "/ui/upload", bytes.NewReader(buf.Bytes()), writer.FormDataContentType())
	assertStatus(t, rec, http.StatusRequestEntityTooLarge)
}

func TestMkdir(t *testing.T) {
	h, lib := newTestServer(t)

	rec := postForm(t, h, "/ui/mkdir", url.Values{
		"dir":  {""},
		"name": {"folder"},
	})
	assertStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/browse/" {
		t.Fatalf("Location = %q, want /browse/", got)
	}
	if _, err := lib.Stat("folder"); err != nil {
		t.Fatalf("created folder missing: %v", err)
	}
}

func TestRenameCollision(t *testing.T) {
	h, lib := newTestServer(t)
	writeDoc(t, lib, "a.md", "# A\n")
	writeDoc(t, lib, "b.md", "# B\n")

	rec := postForm(t, h, "/ui/rename", url.Values{
		"path": {"a.md"},
		"name": {"b.md"},
	})
	assertStatus(t, rec, http.StatusConflict)
	assertContains(t, rec.Body.String(), "already exists")
	if _, err := lib.Stat("a.md"); err != nil {
		t.Fatalf("original document was removed: %v", err)
	}
}

func TestDelete(t *testing.T) {
	h, lib := newTestServer(t)
	writeDoc(t, lib, "gone.md", "# Gone\n")

	rec := postForm(t, h, "/ui/delete", url.Values{"path": {"gone.md"}})
	assertStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/browse/" {
		t.Fatalf("Location = %q, want /browse/", got)
	}
	if _, err := lib.Stat("gone.md"); err == nil {
		t.Fatal("document still exists after delete")
	}
}

func TestDeleteNonEmptyDirectory(t *testing.T) {
	h, lib := newTestServer(t)
	makeDir(t, lib, "folder")
	writeDoc(t, lib, "folder/inside.md", "# Inside\n")

	rec := postForm(t, h, "/ui/delete", url.Values{"path": {"folder"}})
	assertStatus(t, rec, http.StatusConflict)
	assertContains(t, rec.Body.String(), "not empty")
}

func TestTraversalForbidden(t *testing.T) {
	h, _ := newTestServer(t)

	for _, target := range []string{"/browse/../x", "/browse/%2e%2e%2Fx"} {
		rec := do(t, h, http.MethodGet, target, nil, "")
		assertStatus(t, rec, http.StatusForbidden)
		assertContains(t, rec.Body.String(), "403")
	}
}

func TestReaderOnDirectory(t *testing.T) {
	h, lib := newTestServer(t)
	makeDir(t, lib, "folder")

	rec := do(t, h, http.MethodGet, "/read/folder", nil, "")
	assertStatus(t, rec, http.StatusNotFound)
}

func TestNotFound(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/missing", nil, "")
	assertStatus(t, rec, http.StatusNotFound)
	assertContains(t, rec.Body.String(), "404")
}

func TestSecurityHeaders(t *testing.T) {
	h, lib := newTestServer(t)
	writeDoc(t, lib, "doc.md", "# Doc\n")

	rec := do(t, h, http.MethodGet, "/browse/", nil, "")
	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html; charset=utf-8") {
		t.Fatalf("Content-Type = %q", got)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("Content-Security-Policy missing frame-ancestors: %q", csp)
	}
	if !strings.Contains(csp, "img-src 'self' data: https:") {
		t.Fatalf("Content-Security-Policy missing remote image allowance: %q", csp)
	}

	rec = do(t, h, http.MethodGet, "/assets/missing.png", nil, "")
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("asset error X-Content-Type-Options = %q", got)
	}
}
