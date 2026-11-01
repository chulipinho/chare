package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"chare/internal/library"
	"chare/internal/render"
)

type Config struct {
	MaxUpload int64
}

type api struct {
	lib       *library.Library
	rend      *render.Renderer
	maxUpload int64
	mux       *http.ServeMux
}

type entryResponse struct {
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Kind    library.Kind `json:"kind"`
	Size    int64        `json:"size"`
	ModTime string       `json:"modTime"`
}

type treeResponse struct {
	Path    string          `json:"path"`
	Entries []entryResponse `json:"entries"`
}

type uploadResponse struct {
	Uploaded []entryResponse `json:"uploaded"`
	Errors   []uploadError   `json:"errors"`
}

type uploadError struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func New(lib *library.Library, rend *render.Renderer, cfg Config) http.Handler {
	maxUpload := cfg.MaxUpload
	if maxUpload <= 0 {
		maxUpload = 25 << 20
	}

	a := &api{
		lib:       lib,
		rend:      rend,
		maxUpload: maxUpload,
		mux:       http.NewServeMux(),
	}

	a.mux.HandleFunc("GET /api/v1/health", a.handleHealth)
	a.mux.HandleFunc("GET /api/v1/tree", a.handleTree)
	a.mux.HandleFunc("GET /api/v1/files/{path...}", a.handleGetFile)
	a.mux.HandleFunc("PUT /api/v1/files/{path...}", a.handlePutFile)
	a.mux.HandleFunc("PATCH /api/v1/files/{path...}", a.handlePatchFile)
	a.mux.HandleFunc("DELETE /api/v1/files/{path...}", a.handleDeleteFile)
	a.mux.HandleFunc("POST /api/v1/upload", a.handleUpload)
	a.mux.HandleFunc("POST /api/v1/dirs/{path...}", a.handleMkdir)
	a.mux.HandleFunc("DELETE /api/v1/dirs/{path...}", a.handleRmdir)

	a.mux.HandleFunc("/api/v1/health", methodNotAllowed(http.MethodGet))
	a.mux.HandleFunc("/api/v1/tree", methodNotAllowed(http.MethodGet))
	a.mux.HandleFunc("/api/v1/files/{path...}", methodNotAllowed(http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete))
	a.mux.HandleFunc("/api/v1/upload", methodNotAllowed(http.MethodPost))
	a.mux.HandleFunc("/api/v1/dirs/{path...}", methodNotAllowed(http.MethodPost, http.MethodDelete))

	a.mux.HandleFunc("/api/v1/", notFound)
	a.mux.HandleFunc("/", notFound)

	return a
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")

	if isStateChanging(r.Method) && !sameOrigin(r) {
		writeErrorJSON(w, http.StatusForbidden, "forbidden", "cross-origin request forbidden")
		return
	}

	if hasDotSegment(r.URL.Path) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			writeError(w, library.ErrInvalidPath)
		} else {
			writeError(w, library.ErrNotFound)
		}
		return
	}

	a.mux.ServeHTTP(w, r)
}

func hasDotSegment(p string) bool {
	for _, segment := range strings.Split(p, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func sameOrigin(r *http.Request) bool {
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "same-origin":
		return true
	case "cross-site":
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if strings.EqualFold(origin, "null") {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (a *api) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *api) handleTree(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	entries, err := a.lib.List(rel)
	if err != nil {
		writeError(w, err)
		return
	}
	resp := treeResponse{Path: rel, Entries: make([]entryResponse, 0, len(entries))}
	for _, entry := range entries {
		resp.Entries = append(resp.Entries, toEntryResponse(entry))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *api) handleGetFile(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	data, entry, err := a.lib.Read(rel)
	if err != nil {
		writeError(w, err)
		return
	}

	if entry.Kind == library.KindDocument && wantsHTML(r) {
		res, err := a.rend.Render(rel, data)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data: https:; style-src 'unsafe-inline'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if wantsDownload(r) {
			setAttachment(w, rel)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, res.HTML)
		return
	}

	contentType, image := rawContentType(rel)
	w.Header().Set("Content-Type", contentType)
	if image {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	if wantsDownload(r) {
		setAttachment(w, rel)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (a *api) handlePutFile(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	r.Body = http.MaxBytesReader(w, r.Body, a.maxUpload)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, err)
		return
	}

	_, statErr := a.lib.Stat(rel)
	existed := statErr == nil
	overwrite := r.URL.Query().Get("overwrite") == "true"

	entry, err := a.lib.Write(rel, data, overwrite)
	if err != nil {
		writeError(w, err)
		return
	}

	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	writeJSON(w, status, toEntryResponse(entry))
}

func (a *api) handlePatchFile(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	var body struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if body.To == "" {
		writeErrorJSON(w, http.StatusBadRequest, "bad_request", "missing destination path")
		return
	}

	entry, err := a.lib.Rename(rel, body.To, false)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toEntryResponse(entry))
}

func (a *api) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	if err := a.lib.Delete(r.PathValue("path")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.maxUpload+1<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		writeError(w, err)
		return
	}
	defer r.MultipartForm.RemoveAll()

	dir := r.URL.Query().Get("dir")
	if dir == "" {
		if values := r.MultipartForm.Value["dir"]; len(values) > 0 {
			dir = values[0]
		}
	}

	resp := uploadResponse{
		Uploaded: make([]entryResponse, 0),
		Errors:   make([]uploadError, 0),
	}

	for _, header := range r.MultipartForm.File["files"] {
		name := sanitizeFilename(header.Filename)
		if name == "" {
			resp.Errors = append(resp.Errors, uploadError{Name: header.Filename, Error: "invalid filename"})
			continue
		}

		file, err := header.Open()
		if err != nil {
			resp.Errors = append(resp.Errors, uploadError{Name: name, Error: uploadErrorMessage(err)})
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, a.maxUpload+1))
		_ = file.Close()
		if err != nil {
			resp.Errors = append(resp.Errors, uploadError{Name: name, Error: uploadErrorMessage(err)})
			continue
		}
		if int64(len(data)) > a.maxUpload {
			resp.Errors = append(resp.Errors, uploadError{Name: name, Error: "file too large"})
			continue
		}

		target := path.Join(dir, name)
		entry, err := a.lib.Write(target, data, false)
		if err != nil {
			resp.Errors = append(resp.Errors, uploadError{Name: name, Error: uploadErrorMessage(err)})
			continue
		}
		resp.Uploaded = append(resp.Uploaded, toEntryResponse(entry))
	}

	writeJSON(w, http.StatusOK, resp)
}

func (a *api) handleMkdir(w http.ResponseWriter, r *http.Request) {
	entry, err := a.lib.Mkdir(r.PathValue("path"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toEntryResponse(entry))
}

func (a *api) handleRmdir(w http.ResponseWriter, r *http.Request) {
	if err := a.lib.Rmdir(r.PathValue("path")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return ""
	}
	return name
}

func uploadErrorMessage(err error) string {
	switch {
	case errors.Is(err, library.ErrExists):
		return library.ErrExists.Error()
	case errors.Is(err, library.ErrInvalidPath):
		return library.ErrInvalidPath.Error()
	case errors.Is(err, library.ErrUnsupportedType):
		return library.ErrUnsupportedType.Error()
	case errors.Is(err, library.ErrNotFound):
		return library.ErrNotFound.Error()
	case errors.Is(err, library.ErrNotEmpty):
		return library.ErrNotEmpty.Error()
	case errors.Is(err, library.ErrIsDir):
		return library.ErrIsDir.Error()
	case errors.Is(err, library.ErrNotDir):
		return library.ErrNotDir.Error()
	default:
		return "internal error"
	}
}

func wantsHTML(r *http.Request) bool {
	if strings.EqualFold(r.URL.Query().Get("format"), "html") {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

func wantsDownload(r *http.Request) bool {
	return r.URL.Query().Get("download") == "1"
}

func rawContentType(rel string) (string, bool) {
	switch strings.ToLower(path.Ext(rel)) {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8", false
	case ".txt":
		return "text/plain; charset=utf-8", false
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	default:
		return "application/octet-stream", false
	}
}

func setAttachment(w http.ResponseWriter, rel string) {
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(rel)))
}

func toEntryResponse(entry library.Entry) entryResponse {
	return entryResponse{
		Name:    entry.Name,
		Path:    entry.Path,
		Kind:    entry.Kind,
		Size:    entry.Size,
		ModTime: entry.ModTime.Format(time.RFC3339Nano),
	}
}

func methodNotAllowed(methods ...string) http.HandlerFunc {
	allow := strings.Join(methods, ", ")
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeErrorJSON(w, http.StatusNotFound, "not_found", "not found")
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, library.ErrInvalidPath):
		writeErrorJSON(w, http.StatusForbidden, "invalid_path", "invalid path")
	case errors.Is(err, library.ErrNotFound):
		writeErrorJSON(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, library.ErrExists):
		writeErrorJSON(w, http.StatusConflict, "exists", "already exists")
	case errors.Is(err, library.ErrNotEmpty):
		writeErrorJSON(w, http.StatusConflict, "not_empty", "directory not empty")
	case errors.Is(err, library.ErrUnsupportedType):
		writeErrorJSON(w, http.StatusUnsupportedMediaType, "unsupported_type", "unsupported file type")
	case errors.Is(err, library.ErrIsDir):
		writeErrorJSON(w, http.StatusBadRequest, "is_dir", "is a directory")
	case errors.Is(err, library.ErrNotDir):
		writeErrorJSON(w, http.StatusBadRequest, "not_dir", "not a directory")
	default:
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "too_large", "request body too large")
			return
		}
		slog.Error("httpapi request failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "internal", "internal server error")
	}
}

func writeErrorJSON(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
