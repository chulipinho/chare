package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chare/internal/library"
	"chare/internal/render"
)

type testEnv struct {
	lib     *library.Library
	handler http.Handler
	server  *httptest.Server
}

type testResponse struct {
	status int
	header http.Header
	body   []byte
}

func newTestEnv(t *testing.T, maxUpload int64) *testEnv {
	t.Helper()
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	handler := New(lib, render.New(), Config{MaxUpload: maxUpload})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &testEnv{lib: lib, handler: handler, server: server}
}

func (e *testEnv) request(t *testing.T, method, target string, body io.Reader, headers map[string]string) testResponse {
	t.Helper()
	req, err := http.NewRequest(method, e.server.URL+target, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return testResponse{status: resp.StatusCode, header: resp.Header, body: data}
}

func (e *testEnv) serve(t *testing.T, method, target string, body io.Reader) testResponse {
	t.Helper()
	return e.serveWithHeaders(t, method, target, body, nil)
}

func (e *testEnv) serveWithHeaders(t *testing.T, method, target string, body io.Reader, headers map[string]string) testResponse {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return testResponse{status: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
}

func decodeJSON[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode JSON %q: %v", data, err)
	}
	return value
}

func assertStatus(t *testing.T, resp testResponse, want int) {
	t.Helper()
	if resp.status != want {
		t.Fatalf("status = %d, want %d; body = %s", resp.status, want, resp.body)
	}
}

func assertErrorCode(t *testing.T, resp testResponse, wantStatus int, wantCode string) {
	t.Helper()
	assertStatus(t, resp, wantStatus)
	body := decodeJSON[errorResponse](t, resp.body)
	if body.Code != wantCode {
		t.Fatalf("code = %q, want %q; body = %s", body.Code, wantCode, resp.body)
	}
	if body.Error == "" {
		t.Fatal("error message is empty")
	}
	if got := resp.header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func assertCommonHeaders(t *testing.T, resp testResponse) {
	t.Helper()
	if got := resp.header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := resp.header.Get("Cache-Control"); got == "" {
		t.Fatal("Cache-Control is empty")
	}
}

func assertHTMLSecurityHeaders(t *testing.T, resp testResponse) {
	t.Helper()
	if got := resp.header.Get("Content-Security-Policy"); got != "default-src 'none'; img-src 'self' data: https:; style-src 'unsafe-inline'" {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
	if got := resp.header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
	if got := resp.header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

func TestHealth(t *testing.T) {
	env := newTestEnv(t, 0)
	resp := env.request(t, http.MethodGet, "/api/v1/health", nil, nil)
	assertStatus(t, resp, http.StatusOK)
	assertCommonHeaders(t, resp)
	if got := resp.header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	body := decodeJSON[map[string]string](t, resp.body)
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestTree(t *testing.T) {
	env := newTestEnv(t, 0)
	if _, err := env.lib.Mkdir("notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.lib.Write("notes/a.md", []byte("# A"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := env.lib.Write("root.md", []byte("# Root"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := env.lib.Write("pic.png", []byte("png"), false); err != nil {
		t.Fatal(err)
	}

	resp := env.request(t, http.MethodGet, "/api/v1/tree", nil, nil)
	assertStatus(t, resp, http.StatusOK)
	assertCommonHeaders(t, resp)
	tree := decodeJSON[treeResponse](t, resp.body)
	if tree.Path != "" {
		t.Fatalf("path = %q, want empty", tree.Path)
	}
	if len(tree.Entries) != 3 {
		t.Fatalf("entries = %+v", tree.Entries)
	}
	byName := map[string]entryResponse{}
	for _, entry := range tree.Entries {
		byName[entry.Name] = entry
		if _, err := time.Parse(time.RFC3339Nano, entry.ModTime); err != nil {
			t.Fatalf("modTime %q: %v", entry.ModTime, err)
		}
	}
	if byName["notes"].Kind != library.KindDir {
		t.Fatalf("notes entry = %+v", byName["notes"])
	}
	if byName["root.md"].Kind != library.KindDocument {
		t.Fatalf("root.md entry = %+v", byName["root.md"])
	}
	if byName["pic.png"].Kind != library.KindAsset {
		t.Fatalf("pic.png entry = %+v", byName["pic.png"])
	}

	resp = env.request(t, http.MethodGet, "/api/v1/tree?path=notes", nil, nil)
	assertStatus(t, resp, http.StatusOK)
	tree = decodeJSON[treeResponse](t, resp.body)
	if tree.Path != "notes" || len(tree.Entries) != 1 || tree.Entries[0].Path != "notes/a.md" {
		t.Fatalf("tree = %+v", tree)
	}

	assertErrorCode(t, env.request(t, http.MethodGet, "/api/v1/tree?path=missing", nil, nil), http.StatusNotFound, "not_found")
}

func TestFiles(t *testing.T) {
	env := newTestEnv(t, 0)
	if _, err := env.lib.Mkdir("notes"); err != nil {
		t.Fatal(err)
	}
	resp := env.request(t, http.MethodPut, "/api/v1/files/notes/new.md", strings.NewReader("# Hello\n\nBody\n"), nil)
	assertStatus(t, resp, http.StatusCreated)
	assertCommonHeaders(t, resp)
	entry := decodeJSON[entryResponse](t, resp.body)
	if entry.Path != "notes/new.md" {
		t.Fatalf("entry = %+v", entry)
	}

	resp = env.request(t, http.MethodGet, "/api/v1/files/notes/new.md", nil, nil)
	assertStatus(t, resp, http.StatusOK)
	if got := resp.header.Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if string(resp.body) != "# Hello\n\nBody\n" {
		t.Fatalf("body = %q", resp.body)
	}

	resp = env.request(t, http.MethodGet, "/api/v1/files/notes/new.md?format=html", nil, nil)
	assertStatus(t, resp, http.StatusOK)
	if got := resp.header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !strings.Contains(string(resp.body), "<h1") {
		t.Fatalf("rendered HTML = %s", resp.body)
	}
	assertHTMLSecurityHeaders(t, resp)

	resp = env.request(t, http.MethodGet, "/api/v1/files/notes/new.md", nil, map[string]string{"Accept": "text/html"})
	assertStatus(t, resp, http.StatusOK)
	if !strings.Contains(string(resp.body), "<h1") {
		t.Fatalf("Accept negotiation body = %s", resp.body)
	}
	assertHTMLSecurityHeaders(t, resp)

	resp = env.request(t, http.MethodGet, "/api/v1/files/notes/new.md", nil, map[string]string{"Accept": "text/plain"})
	assertStatus(t, resp, http.StatusOK)
	if string(resp.body) != "# Hello\n\nBody\n" {
		t.Fatalf("raw body = %q", resp.body)
	}

	resp = env.request(t, http.MethodGet, "/api/v1/files/notes/new.md?download=1", nil, nil)
	assertStatus(t, resp, http.StatusOK)
	if got := resp.header.Get("Content-Disposition"); got != `attachment; filename="new.md"` {
		t.Fatalf("Content-Disposition = %q", got)
	}

	resp = env.request(t, http.MethodPut, "/api/v1/files/notes/new.md", strings.NewReader("collision"), nil)
	assertErrorCode(t, resp, http.StatusConflict, "exists")

	resp = env.request(t, http.MethodPut, "/api/v1/files/notes/new.md?overwrite=true", strings.NewReader("# Replaced\n"), nil)
	assertStatus(t, resp, http.StatusOK)
	resp = env.request(t, http.MethodGet, "/api/v1/files/notes/new.md", nil, nil)
	if string(resp.body) != "# Replaced\n" {
		t.Fatalf("replaced body = %q", resp.body)
	}

	resp = env.request(t, http.MethodPut, "/api/v1/files/note.txt", strings.NewReader("plain"), nil)
	assertStatus(t, resp, http.StatusCreated)
	resp = env.request(t, http.MethodGet, "/api/v1/files/note.txt", nil, nil)
	if got := resp.header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("txt Content-Type = %q", got)
	}

	resp = env.request(t, http.MethodPut, "/api/v1/files/pic.png", strings.NewReader("png"), nil)
	assertStatus(t, resp, http.StatusCreated)
	resp = env.request(t, http.MethodGet, "/api/v1/files/pic.png", nil, nil)
	if got := resp.header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("png Content-Type = %q", got)
	}
	if got := resp.header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("png Cache-Control = %q", got)
	}
}

func multipartBody(t *testing.T, dir string, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if dir != "" {
		if err := writer.WriteField("dir", dir); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, writer.FormDataContentType()
}

func TestUpload(t *testing.T) {
	env := newTestEnv(t, 0)
	if _, err := env.lib.Mkdir("uploads"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.lib.Write("uploads/dup.md", []byte("old"), false); err != nil {
		t.Fatal(err)
	}

	body, contentType := multipartBody(t, "", map[string]string{
		"a.md":        "# A\n",
		"dup.md":      "# duplicate\n",
		`dir\evil.md`: "# Evil\n",
	})
	resp := env.request(t, http.MethodPost, "/api/v1/upload?dir=uploads", body, map[string]string{"Content-Type": contentType})
	assertStatus(t, resp, http.StatusOK)
	assertCommonHeaders(t, resp)
	result := decodeJSON[uploadResponse](t, resp.body)
	if len(result.Uploaded) != 2 {
		t.Fatalf("uploaded = %+v; errors = %+v", result.Uploaded, result.Errors)
	}
	if len(result.Errors) != 1 || result.Errors[0].Name != "dup.md" || result.Errors[0].Error != "already exists" {
		t.Fatalf("errors = %+v", result.Errors)
	}
	names := map[string]bool{}
	for _, entry := range result.Uploaded {
		names[entry.Name] = true
	}
	if !names["a.md"] || !names["evil.md"] {
		t.Fatalf("uploaded names = %v", names)
	}

	resp = env.request(t, http.MethodGet, "/api/v1/files/uploads/a.md", nil, nil)
	assertStatus(t, resp, http.StatusOK)
	if string(resp.body) != "# A\n" {
		t.Fatalf("uploaded body = %q", resp.body)
	}
	resp = env.request(t, http.MethodGet, "/api/v1/files/uploads/dup.md", nil, nil)
	if string(resp.body) != "old" {
		t.Fatalf("duplicate overwritten: %q", resp.body)
	}

	if _, err := env.lib.Mkdir("second"); err != nil {
		t.Fatal(err)
	}
	body, contentType = multipartBody(t, "second", map[string]string{"b.md": "# B\n"})
	resp = env.request(t, http.MethodPost, "/api/v1/upload", body, map[string]string{"Content-Type": contentType})
	assertStatus(t, resp, http.StatusOK)
	result = decodeJSON[uploadResponse](t, resp.body)
	if len(result.Uploaded) != 1 || result.Uploaded[0].Path != "second/b.md" {
		t.Fatalf("upload with form dir = %+v", result)
	}
}

func TestRenameAndDelete(t *testing.T) {
	env := newTestEnv(t, 0)
	resp := env.request(t, http.MethodPut, "/api/v1/files/old.md", strings.NewReader("# Old\n"), nil)
	assertStatus(t, resp, http.StatusCreated)

	resp = env.request(t, http.MethodPatch, "/api/v1/files/old.md", strings.NewReader(`{"to":"renamed.md","ignored":true}`), map[string]string{"Content-Type": "application/json"})
	assertStatus(t, resp, http.StatusOK)
	entry := decodeJSON[entryResponse](t, resp.body)
	if entry.Path != "renamed.md" {
		t.Fatalf("renamed entry = %+v", entry)
	}

	assertErrorCode(t, env.request(t, http.MethodGet, "/api/v1/files/old.md", nil, nil), http.StatusNotFound, "not_found")

	resp = env.request(t, http.MethodDelete, "/api/v1/files/renamed.md", nil, nil)
	assertStatus(t, resp, http.StatusNoContent)
	if len(resp.body) != 0 {
		t.Fatalf("delete body = %q", resp.body)
	}
	assertErrorCode(t, env.request(t, http.MethodGet, "/api/v1/files/renamed.md", nil, nil), http.StatusNotFound, "not_found")
}

func TestDirs(t *testing.T) {
	env := newTestEnv(t, 0)
	resp := env.request(t, http.MethodPost, "/api/v1/dirs/mydir", nil, nil)
	assertStatus(t, resp, http.StatusCreated)
	entry := decodeJSON[entryResponse](t, resp.body)
	if entry.Path != "mydir" || entry.Kind != library.KindDir {
		t.Fatalf("mkdir entry = %+v", entry)
	}
	assertErrorCode(t, env.request(t, http.MethodPost, "/api/v1/dirs/mydir", nil, nil), http.StatusConflict, "exists")

	resp = env.request(t, http.MethodPut, "/api/v1/files/mydir/a.md", strings.NewReader("x"), nil)
	assertStatus(t, resp, http.StatusCreated)
	assertErrorCode(t, env.request(t, http.MethodDelete, "/api/v1/dirs/mydir", nil, nil), http.StatusConflict, "not_empty")

	resp = env.request(t, http.MethodDelete, "/api/v1/files/mydir/a.md", nil, nil)
	assertStatus(t, resp, http.StatusNoContent)
	resp = env.request(t, http.MethodDelete, "/api/v1/dirs/mydir", nil, nil)
	assertStatus(t, resp, http.StatusNoContent)

	resp = env.serve(t, http.MethodPost, "/api/v1/dirs/", nil)
	assertErrorCode(t, resp, http.StatusForbidden, "invalid_path")
}

func TestTraversal(t *testing.T) {
	env := newTestEnv(t, 0)
	assertErrorCode(t, env.request(t, http.MethodGet, "/api/v1/tree?path=../x", nil, nil), http.StatusForbidden, "invalid_path")

	tests := []struct {
		method string
		target string
	}{
		{http.MethodGet, "/api/v1/files/../x"},
		{http.MethodDelete, "/api/v1/files/../x"},
		{http.MethodDelete, "/api/v1/dirs/../x"},
	}
	for _, test := range tests {
		resp := env.serve(t, test.method, test.target, nil)
		assertErrorCode(t, resp, http.StatusForbidden, "invalid_path")
	}
}

func TestErrors(t *testing.T) {
	env := newTestEnv(t, 0)
	assertErrorCode(t, env.request(t, http.MethodPut, "/api/v1/files/bad.html", strings.NewReader("x"), nil), http.StatusUnsupportedMediaType, "unsupported_type")

	resp := env.request(t, http.MethodPatch, "/api/v1/files/missing.md", strings.NewReader("{"), map[string]string{"Content-Type": "application/json"})
	assertErrorCode(t, resp, http.StatusBadRequest, "bad_request")

	assertErrorCode(t, env.request(t, http.MethodGet, "/api/v1/nope", nil, nil), http.StatusNotFound, "not_found")
	assertErrorCode(t, env.request(t, http.MethodGet, "/nope", nil, nil), http.StatusNotFound, "not_found")

	resp = env.request(t, http.MethodPut, "/api/v1/health", nil, nil)
	assertErrorCode(t, resp, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := resp.header.Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q", got)
	}
}

func TestOversize(t *testing.T) {
	env := newTestEnv(t, 8)
	resp := env.request(t, http.MethodPut, "/api/v1/files/big.md", strings.NewReader(strings.Repeat("x", 9)), nil)
	assertErrorCode(t, resp, http.StatusRequestEntityTooLarge, "too_large")

	body, contentType := multipartBody(t, "", map[string]string{"big.md": strings.Repeat("x", 9)})
	resp = env.request(t, http.MethodPost, "/api/v1/upload", body, map[string]string{"Content-Type": contentType})
	assertStatus(t, resp, http.StatusOK)
	result := decodeJSON[uploadResponse](t, resp.body)
	if len(result.Uploaded) != 0 {
		t.Fatalf("uploaded = %+v", result.Uploaded)
	}
	if len(result.Errors) != 1 || result.Errors[0].Name != "big.md" || result.Errors[0].Error != "file too large" {
		t.Fatalf("errors = %+v", result.Errors)
	}
	assertErrorCode(t, env.request(t, http.MethodGet, "/api/v1/files/big.md", nil, nil), http.StatusNotFound, "not_found")

	body, contentType = multipartBody(t, "", map[string]string{
		"ok.md":  "12345678",
		"big.md": strings.Repeat("x", 9),
	})
	resp = env.request(t, http.MethodPost, "/api/v1/upload", body, map[string]string{"Content-Type": contentType})
	assertStatus(t, resp, http.StatusOK)
	result = decodeJSON[uploadResponse](t, resp.body)
	if len(result.Uploaded) != 1 || result.Uploaded[0].Name != "ok.md" {
		t.Fatalf("uploaded = %+v", result.Uploaded)
	}
	if len(result.Errors) != 1 || result.Errors[0].Name != "big.md" || result.Errors[0].Error != "file too large" {
		t.Fatalf("errors = %+v", result.Errors)
	}

	body, contentType = multipartBody(t, "", map[string]string{"huge.md": strings.Repeat("x", 1<<20+1024)})
	resp = env.serveWithHeaders(t, http.MethodPost, "/api/v1/upload", body, map[string]string{"Content-Type": contentType})
	assertErrorCode(t, resp, http.StatusRequestEntityTooLarge, "too_large")
}

func TestCrossOriginGuard(t *testing.T) {
	env := newTestEnv(t, 0)
	if _, err := env.lib.Write("victim.md", []byte("keep"), false); err != nil {
		t.Fatal(err)
	}
	forged := map[string]string{"Origin": "http://evil.example"}

	resp := env.request(t, http.MethodPut, "/api/v1/files/blocked.md", strings.NewReader("x"), forged)
	assertErrorCode(t, resp, http.StatusForbidden, "forbidden")
	assertErrorCode(t, env.request(t, http.MethodGet, "/api/v1/files/blocked.md", nil, nil), http.StatusNotFound, "not_found")

	body, contentType := multipartBody(t, "", map[string]string{"blocked.md": "x"})
	resp = env.request(t, http.MethodPost, "/api/v1/upload", body, map[string]string{
		"Origin":       "http://evil.example",
		"Content-Type": contentType,
	})
	assertErrorCode(t, resp, http.StatusForbidden, "forbidden")
	assertErrorCode(t, env.request(t, http.MethodGet, "/api/v1/files/blocked.md", nil, nil), http.StatusNotFound, "not_found")

	resp = env.request(t, http.MethodDelete, "/api/v1/files/victim.md", nil, forged)
	assertErrorCode(t, resp, http.StatusForbidden, "forbidden")
	assertStatus(t, env.request(t, http.MethodGet, "/api/v1/files/victim.md", nil, nil), http.StatusOK)

	resp = env.request(t, http.MethodPost, "/api/v1/dirs/blocked", nil, map[string]string{"Sec-Fetch-Site": "cross-site"})
	assertErrorCode(t, resp, http.StatusForbidden, "forbidden")

	resp = env.request(t, http.MethodPost, "/api/v1/dirs/null-same-origin", nil, map[string]string{
		"Origin":         "null",
		"Sec-Fetch-Site": "same-origin",
	})
	assertStatus(t, resp, http.StatusCreated)
	if _, err := env.lib.Stat("null-same-origin"); err != nil {
		t.Fatalf("null-origin same-origin request did not create a directory: %v", err)
	}

	resp = env.request(t, http.MethodPost, "/api/v1/dirs/null-no-metadata", nil, map[string]string{"Origin": "null"})
	assertErrorCode(t, resp, http.StatusForbidden, "forbidden")
	if _, err := env.lib.Stat("null-no-metadata"); err == nil {
		t.Fatal("null-origin request created a directory")
	}

	resp = env.request(t, http.MethodPost, "/api/v1/dirs/proxy-rewrite", nil, map[string]string{
		"Origin":         "http://proxy.example",
		"Sec-Fetch-Site": "same-origin",
	})
	assertStatus(t, resp, http.StatusCreated)
	if _, err := env.lib.Stat("proxy-rewrite"); err != nil {
		t.Fatalf("rewritten-host request did not create a directory: %v", err)
	}

	resp = env.request(t, http.MethodPut, "/api/v1/files/same.md", strings.NewReader("x"), map[string]string{"Origin": env.server.URL})
	assertStatus(t, resp, http.StatusCreated)

	resp = env.request(t, http.MethodGet, "/api/v1/files/same.md", nil, forged)
	assertStatus(t, resp, http.StatusOK)
}
