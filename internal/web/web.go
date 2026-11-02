package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"chare/internal/library"
	"chare/internal/render"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Config struct {
	MaxUpload int64
}

type crumb struct {
	Name string
	Path string
}

type basePage struct {
	Title          string
	BodyClass      string
	Breadcrumbs    []crumb
	Path           string
	WrapCodeToggle bool
}

type browsePage struct {
	basePage
	Dirs      []library.Entry
	Documents []library.Entry
	Assets    []library.Entry
}

type readPage struct {
	basePage
	DocTitle string
	Size     string
	Headings []render.Heading
	HTML     template.HTML
}

type simplePage struct {
	Title  string
	Parent string
	HTML   template.HTML
}

type formFailure struct {
	Name   string
	Reason string
	Status int
}

type errorPage struct {
	Status         int
	Title          string
	BodyClass      string
	Message        string
	Back           string
	Failures       []formFailure
	WrapCodeToggle bool
}

type handler struct {
	lib       *library.Library
	rend      *render.Renderer
	maxUpload int64
	templates map[string]*template.Template
	static    http.Handler
}

func New(lib *library.Library, rend *render.Renderer, cfg Config) (http.Handler, error) {
	if lib == nil {
		return nil, errors.New("web: library is required")
	}
	if rend == nil {
		return nil, errors.New("web: renderer is required")
	}

	maxUpload := cfg.MaxUpload
	if maxUpload <= 0 {
		maxUpload = 25 << 20
	}

	styleVersion, err := staticVersion("style.css")
	if err != nil {
		return nil, err
	}

	funcs := template.FuncMap{
		"pathEscape":   escapePath,
		"browseHref":   browseHref,
		"readHref":     readHref,
		"simpleHref":   simpleHref,
		"rawHref":      rawHref,
		"assetHref":    assetHref,
		"staticHref":   staticHref(styleVersion),
		"humanSize":    humanSize,
		"formatTime":   formatTime,
		"parentBrowse": parentBrowse,
	}

	templates := make(map[string]*template.Template, 4)
	for _, name := range []string{"browse", "read", "error"} {
		t, err := template.New(name).Funcs(funcs).ParseFS(
			templateFS,
			"templates/layout.html",
			"templates/"+name+".html",
		)
		if err != nil {
			return nil, fmt.Errorf("web: parse %s template: %w", name, err)
		}
		templates[name] = t
	}

	simple, err := template.New("simple").Funcs(funcs).ParseFS(templateFS, "templates/simple.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse simple template: %w", err)
	}
	templates["simple"] = simple

	staticRoot, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("web: static files: %w", err)
	}
	fileServer := http.FileServerFS(staticRoot)

	h := &handler{
		lib:       lib,
		rend:      rend,
		maxUpload: maxUpload,
		templates: templates,
		static: http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			fileServer.ServeHTTP(w, r)
		})),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", redirectHandler("/browse/"))
	mux.HandleFunc("GET /browse", redirectHandler("/browse/"))
	mux.HandleFunc("GET /browse/{path...}", h.browse)
	mux.HandleFunc("GET /read/{path...}", h.read)
	mux.HandleFunc("GET /simple/{path...}", h.simple)
	mux.HandleFunc("GET /raw/{path...}", h.raw)
	mux.HandleFunc("GET /assets/{path...}", h.asset)
	mux.Handle("GET /static/{path...}", h.static)
	mux.HandleFunc("POST /ui/upload", h.upload)
	mux.HandleFunc("POST /ui/new", h.newDocument)
	mux.HandleFunc("POST /ui/mkdir", h.mkdir)
	mux.HandleFunc("POST /ui/rename", h.rename)
	mux.HandleFunc("POST /ui/delete", h.delete)
	mux.HandleFunc("/", h.notFound)

	return securityHeaders(h.traversalGuard(h.csrfGuard(mux))), nil
}

func redirectHandler(location string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, location, http.StatusSeeOther)
	}
}

func staticVersion(name string) (string, error) {
	data, err := fs.ReadFile(staticFS, "static/"+name)
	if err != nil {
		return "", fmt.Errorf("web: read static %s: %w", name, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))[:12], nil
}

func staticHref(version string) func(string) string {
	return func(name string) string {
		return "/static/" + name + "?v=" + version
	}
}

const contentSecurityPolicy = "default-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "same-origin")
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

func (h *handler) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !sameOrigin(r) {
			h.fail(w, r, http.StatusForbidden, "This request was blocked for security reasons.", "")
			return
		}
		next.ServeHTTP(w, r)
	})
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

func (h *handler) traversalGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, segment := range strings.Split(r.URL.Path, "/") {
			if segment == "." || segment == ".." {
				h.fail(w, r, http.StatusForbidden, "The requested path is not allowed.", "")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) notFound(w http.ResponseWriter, r *http.Request) {
	h.fail(w, r, http.StatusNotFound, "The requested page was not found.", "")
}

func (h *handler) browse(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSuffix(r.PathValue("path"), "/")
	entries, err := h.lib.List(p)
	if err != nil {
		h.failLibrary(w, r, p, err)
		return
	}

	page := browsePage{basePage: basePage{
		Title:       displayName(p),
		BodyClass:   "browse",
		Breadcrumbs: breadcrumbs(p),
		Path:        p,
	}}
	for _, entry := range entries {
		switch entry.Kind {
		case library.KindDir:
			page.Dirs = append(page.Dirs, entry)
		case library.KindDocument:
			page.Documents = append(page.Documents, entry)
		case library.KindAsset:
			page.Assets = append(page.Assets, entry)
		}
	}
	h.renderHTML(w, r, http.StatusOK, "browse", page)
}

func (h *handler) read(w http.ResponseWriter, r *http.Request) {
	p, result, ok := h.document(w, r)
	if !ok {
		return
	}

	title := result.Title
	if title == "" {
		title = titleFromPath(p)
	}

	size := r.URL.Query().Get("size")
	switch size {
	case "sm", "md", "lg":
	default:
		size = "md"
	}

	page := readPage{
		basePage: basePage{
			Title:          title,
			BodyClass:      "reader size-" + size,
			Breadcrumbs:    breadcrumbs(parentBrowse(p)),
			Path:           p,
			WrapCodeToggle: true,
		},
		DocTitle: title,
		Size:     size,
		Headings: result.Headings,
		HTML:     template.HTML(result.HTML),
	}
	h.renderHTML(w, r, http.StatusOK, "read", page)
}

func (h *handler) simple(w http.ResponseWriter, r *http.Request) {
	p, result, ok := h.document(w, r)
	if !ok {
		return
	}

	title := result.Title
	if title == "" {
		title = titleFromPath(p)
	}

	page := simplePage{
		Title:  title,
		Parent: parentBrowse(p),
		HTML:   template.HTML(result.HTML),
	}
	h.renderHTML(w, r, http.StatusOK, "simple", page)
}

func (h *handler) document(w http.ResponseWriter, r *http.Request) (string, render.Result, bool) {
	p := r.PathValue("path")
	data, entry, err := h.lib.Read(p)
	if err != nil {
		h.failLibrary(w, r, p, err)
		return "", render.Result{}, false
	}
	if entry.Kind != library.KindDocument {
		h.fail(w, r, http.StatusNotFound, "The requested document was not found.", parentBrowse(p))
		return "", render.Result{}, false
	}
	result, err := h.rend.Render(p, data)
	if err != nil {
		h.fail(w, r, http.StatusInternalServerError, "The document could not be rendered.", parentBrowse(p))
		return "", render.Result{}, false
	}
	return p, result, true
}

func (h *handler) raw(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	data, entry, err := h.lib.Read(p)
	if err != nil {
		h.failLibrary(w, r, p, err)
		return
	}
	if entry.Kind != library.KindDocument {
		h.fail(w, r, http.StatusNotFound, "The requested document was not found.", parentBrowse(p))
		return
	}

	contentType := "text/markdown; charset=utf-8"
	if strings.EqualFold(path.Ext(p), ".txt") {
		contentType = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *handler) asset(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	data, entry, err := h.lib.Read(p)
	if err != nil {
		h.failLibrary(w, r, p, err)
		return
	}
	if entry.Kind != library.KindAsset {
		h.fail(w, r, http.StatusNotFound, "The requested asset was not found.", parentBrowse(p))
		return
	}

	var contentType string
	switch strings.ToLower(path.Ext(p)) {
	case ".png":
		contentType = "image/png"
	case ".jpg", ".jpeg":
		contentType = "image/jpeg"
	case ".gif":
		contentType = "image/gif"
	case ".webp":
		contentType = "image/webp"
	default:
		contentType = mime.TypeByExtension(strings.ToLower(path.Ext(p)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *handler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUpload+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			h.fail(w, r, http.StatusRequestEntityTooLarge, "The upload is too large.", "")
			return
		}
		h.fail(w, r, http.StatusBadRequest, "The upload could not be read.", "")
		return
	}

	dir := r.FormValue("dir")
	if r.MultipartForm == nil || len(r.MultipartForm.File["files"]) == 0 {
		h.fail(w, r, http.StatusBadRequest, "No files were selected.", dir)
		return
	}

	var failures []formFailure
	for _, header := range r.MultipartForm.File["files"] {
		name := sanitizeFilename(header.Filename)
		if name == "" {
			failures = append(failures, formFailure{Name: header.Filename, Reason: "Invalid filename.", Status: http.StatusBadRequest})
			continue
		}

		src, err := header.Open()
		if err != nil {
			failures = append(failures, formFailure{Name: name, Reason: "The file could not be read.", Status: http.StatusBadRequest})
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(src, h.maxUpload+1))
		_ = src.Close()
		if readErr != nil {
			status := http.StatusBadRequest
			var maxErr *http.MaxBytesError
			if errors.As(readErr, &maxErr) {
				status = http.StatusRequestEntityTooLarge
			}
			failures = append(failures, formFailure{Name: name, Reason: "The file could not be read.", Status: status})
			continue
		}
		if int64(len(data)) > h.maxUpload {
			failures = append(failures, formFailure{Name: name, Reason: "The file is too large.", Status: http.StatusRequestEntityTooLarge})
			continue
		}

		if _, err := h.lib.Write(path.Join(dir, name), data, false); err != nil {
			status, reason := describeError(err)
			failures = append(failures, formFailure{Name: name, Reason: reason, Status: status})
		}
	}

	if len(failures) > 0 {
		status := uploadStatus(failures)
		h.failWithFailures(w, r, status, "Some files could not be saved.", dir, failures)
		return
	}
	h.seeOther(w, r, browseHref(dir))
}

func (h *handler) newDocument(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, http.StatusBadRequest, "The form could not be read.", "")
		return
	}

	dir := r.FormValue("dir")
	name := strings.TrimSpace(r.FormValue("name"))
	content := r.FormValue("content")
	if !validName(name) {
		h.fail(w, r, http.StatusBadRequest, "Enter a filename without slashes.", dir)
		return
	}
	if path.Ext(name) == "" {
		name += ".md"
	}

	full := path.Join(dir, name)
	if _, err := h.lib.Write(full, []byte(content), false); err != nil {
		h.failForm(w, r, dir, err)
		return
	}
	h.seeOther(w, r, readHref(full))
}

func (h *handler) mkdir(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, http.StatusBadRequest, "The form could not be read.", "")
		return
	}

	dir := r.FormValue("dir")
	name := strings.TrimSpace(r.FormValue("name"))
	if !validName(name) {
		h.fail(w, r, http.StatusBadRequest, "Enter a folder name without slashes.", dir)
		return
	}

	if _, err := h.lib.Mkdir(path.Join(dir, name)); err != nil {
		h.failForm(w, r, dir, err)
		return
	}
	h.seeOther(w, r, browseHref(dir))
}

func (h *handler) rename(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, http.StatusBadRequest, "The form could not be read.", "")
		return
	}

	p := r.FormValue("path")
	name := strings.TrimSpace(r.FormValue("name"))
	parent := parentBrowse(p)
	if !validName(name) {
		h.fail(w, r, http.StatusBadRequest, "Enter a name without slashes.", parent)
		return
	}

	target := path.Join(path.Dir(p), name)
	if _, err := h.lib.Rename(p, target, false); err != nil {
		h.failForm(w, r, parent, err)
		return
	}
	h.seeOther(w, r, browseHref(parent))
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, http.StatusBadRequest, "The form could not be read.", "")
		return
	}

	p := r.FormValue("path")
	parent := parentBrowse(p)
	entry, err := h.lib.Stat(p)
	if err != nil {
		h.failForm(w, r, parent, err)
		return
	}

	if entry.Kind == library.KindDir {
		err = h.lib.Rmdir(p)
	} else {
		err = h.lib.Delete(p)
	}
	if err != nil {
		h.failForm(w, r, parent, err)
		return
	}
	h.seeOther(w, r, browseHref(parent))
}

func (h *handler) failLibrary(w http.ResponseWriter, r *http.Request, p string, err error) {
	status, message := describeError(err)
	back := parentBrowse(p)
	if errors.Is(err, library.ErrInvalidPath) {
		back = ""
	}
	h.fail(w, r, status, message, back)
}

func (h *handler) failForm(w http.ResponseWriter, r *http.Request, back string, err error) {
	status, message := describeError(err)
	h.fail(w, r, status, message, back)
}

func (h *handler) fail(w http.ResponseWriter, r *http.Request, status int, message, back string) {
	h.renderError(w, r, status, message, back, nil)
}

func (h *handler) failWithFailures(w http.ResponseWriter, r *http.Request, status int, message, back string, failures []formFailure) {
	h.renderError(w, r, status, message, back, failures)
}

func (h *handler) renderError(w http.ResponseWriter, r *http.Request, status int, message, back string, failures []formFailure) {
	page := errorPage{
		Status:    status,
		Title:     http.StatusText(status),
		BodyClass: "error",
		Message:   message,
		Back:      back,
		Failures:  failures,
	}
	h.renderHTML(w, r, status, "error", page)
}

func (h *handler) seeOther(w http.ResponseWriter, r *http.Request, location string) {
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func (h *handler) renderHTML(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	t, ok := h.templates[name]
	if !ok {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	execName := name
	if name != "simple" {
		execName = "layout"
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, execName, data); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func describeError(err error) (int, string) {
	switch {
	case errors.Is(err, library.ErrInvalidPath):
		return http.StatusForbidden, "The requested path is not allowed."
	case errors.Is(err, library.ErrNotFound):
		return http.StatusNotFound, "The requested item was not found."
	case errors.Is(err, library.ErrExists):
		return http.StatusConflict, "An item with that name already exists."
	case errors.Is(err, library.ErrUnsupportedType):
		return http.StatusUnsupportedMediaType, "That file type is not supported."
	case errors.Is(err, library.ErrNotEmpty):
		return http.StatusConflict, "The folder is not empty."
	case errors.Is(err, library.ErrIsDir), errors.Is(err, library.ErrNotDir):
		return http.StatusNotFound, "The requested item is not available."
	default:
		return http.StatusInternalServerError, "Something went wrong."
	}
}

func uploadStatus(failures []formFailure) int {
	status := http.StatusBadRequest
	for _, failure := range failures {
		switch failure.Status {
		case http.StatusRequestEntityTooLarge:
			return http.StatusRequestEntityTooLarge
		case http.StatusUnsupportedMediaType:
			status = http.StatusUnsupportedMediaType
		case http.StatusConflict:
			if status != http.StatusUnsupportedMediaType {
				status = http.StatusConflict
			}
		}
	}
	return status
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = path.Base(name)
	if name == "." || name == ".." || name == "/" || name == "" {
		return ""
	}
	return name
}

func displayName(p string) string {
	if p == "" {
		return "Library"
	}
	return path.Base(p)
}

func titleFromPath(p string) string {
	base := path.Base(p)
	if ext := path.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" || base == "." {
		return "Document"
	}
	return base
}

func parentBrowse(p string) string {
	dir := path.Dir(p)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

func breadcrumbs(p string) []crumb {
	crumbs := []crumb{{Name: "Library", Path: ""}}
	if p == "" {
		return crumbs
	}
	var parts []string
	for _, segment := range strings.Split(p, "/") {
		parts = append(parts, segment)
		crumbs = append(crumbs, crumb{Name: segment, Path: strings.Join(parts, "/")})
	}
	return crumbs
}

func escapePath(p string) string {
	segments := strings.Split(p, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func browseHref(p string) string {
	if p == "" {
		return "/browse/"
	}
	return "/browse/" + escapePath(p)
}

func readHref(p string) string {
	return "/read/" + escapePath(p)
}

func simpleHref(p string) string {
	return "/simple/" + escapePath(p)
}

func rawHref(p string) string {
	return "/raw/" + escapePath(p)
}

func assetHref(p string) string {
	return "/assets/" + escapePath(p)
}

func humanSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	value := float64(size)
	unit := "B"
	for _, next := range units {
		value /= 1024
		unit = next
		if value < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}
