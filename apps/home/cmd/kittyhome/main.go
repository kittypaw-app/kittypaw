package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kittypaw-app/kittyhome/internal/config"
	"github.com/kittypaw-app/kittyhome/internal/server"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.Version = buildValue(cfg.Version, version)
	cfg.Commit = buildValue(cfg.Commit, commit)

	router := server.NewRouter(server.Config{
		Version: cfg.Version,
		Commit:  cfg.Commit,
	})

	log.Printf("listening on %s", cfg.BindAddr)
	srv := &http.Server{Addr: cfg.BindAddr, Handler: router}
	if err := serveHTTP(srv, cfg.BindAddr); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func buildValue(configured, built string) string {
	if built != "" && built != "dev" && built != "unknown" {
		return built
	}
	if configured != "" {
		return configured
	}
	if built != "" {
		return built
	}
	return configured
}

func serveHTTP(srv *http.Server, bindAddr string) error {
	socketPath, ok := unixSocketPath(bindAddr)
	if !ok {
		return srv.ListenAndServe()
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer os.Remove(socketPath)
	if err := os.Chmod(socketPath, 0o666); err != nil {
		ln.Close()
		return err
	}
	return srv.Serve(ln)
}

func unixSocketPath(bindAddr string) (string, bool) {
	if strings.HasPrefix(bindAddr, "unix:") {
		return strings.TrimPrefix(bindAddr, "unix:"), true
	}
	if strings.HasPrefix(bindAddr, "/") {
		return bindAddr, true
	}
	return "", false
}
