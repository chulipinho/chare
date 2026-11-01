package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"chare/internal/httpapi"
	"chare/internal/library"
	"chare/internal/render"
	"chare/internal/web"
)

func main() {
	addr := flag.String("addr", env("CHARE_ADDR", ":8080"), "listen address")
	dataDir := flag.String("data", env("CHARE_DATA_DIR", "data"), "library directory")
	maxUpload := flag.Int64("max-upload", envInt64("CHARE_MAX_UPLOAD", 25<<20), "maximum upload size in bytes")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	lib, err := library.Open(*dataDir)
	if err != nil {
		logger.Error("open library", "error", err)
		os.Exit(1)
	}

	rend := render.New()

	api := httpapi.New(lib, rend, httpapi.Config{MaxUpload: *maxUpload})
	ui, err := web.New(lib, rend, web.Config{MaxUpload: *maxUpload})
	if err != nil {
		logger.Error("build web ui", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", api)
	mux.Handle("/", ui)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           logRequests(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	logger.Info("listening", "addr", *addr, "data", lib.Root())

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown", "error", err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve", "error", err)
			os.Exit(1)
		}
	}
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration", time.Since(start))
	})
}
