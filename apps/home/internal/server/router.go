package server

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed web/*
var staticAssets embed.FS

type Config struct {
	Version    string
	Commit     string
	WebHandler interface {
		MountRoutes(chi.Router)
	}
	DaemonHandler interface {
		Routes() http.Handler
	}
	OpenAIHandler interface {
		Routes() http.Handler
	}
}

func NewRouter(cfg Config) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		version := cfg.Version
		if version == "" {
			version = "dev"
		}
		commit := cfg.Commit
		if commit == "" {
			commit = "unknown"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "healthy",
			"version": version,
			"commit":  commit,
		})
	})
	if cfg.WebHandler != nil {
		cfg.WebHandler.MountRoutes(r)
	}
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chat/", http.StatusFound)
	})
	r.Get("/chat", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chat/", http.StatusMovedPermanently)
	})
	r.Get("/chat/", serveStaticFile("web/chat.html"))
	r.Handle("/assets/*", http.StripPrefix("/assets/", assetHandler()))
	if cfg.OpenAIHandler != nil {
		r.Mount("/", cfg.OpenAIHandler.Routes())
	}
	if cfg.DaemonHandler != nil {
		r.Mount("/daemon", cfg.DaemonHandler.Routes())
	}
	return r
}

func serveStaticFile(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.ServeFileFS(w, r, staticAssets, name)
	}
}

func assetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		if name == "" || strings.Contains(name, "/") || (!strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".css")) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFileFS(w, r, staticAssets, "web/"+name)
	})
}
