package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kittypaw-app/kittyhome/internal/broker"
	"github.com/kittypaw-app/kittyhome/internal/config"
	"github.com/kittypaw-app/kittyhome/internal/daemonws"
	"github.com/kittypaw-app/kittyhome/internal/identity"
	"github.com/kittypaw-app/kittyhome/internal/openai"
	"github.com/kittypaw-app/kittyhome/internal/server"
	"github.com/kittypaw-app/kittyhome/internal/webapp"
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

	router, err := newRouter(cfg)
	if err != nil {
		log.Fatalf("router: %v", err)
	}

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

func newRouter(cfg config.Config) (http.Handler, error) {
	verifier, err := newCredentialVerifier(cfg)
	if err != nil {
		return nil, err
	}
	b := broker.New(broker.Config{})
	openAIHandler := openai.NewHandler(identity.APIAuthenticator{
		Verifier: verifier,
	}, b)
	hostedWebHandler, err := webapp.New(webapp.Config{
		PublicBaseURL:  cfg.PublicBaseURL,
		APIAuthBaseURL: cfg.APIAuthBaseURL,
		Verifier:       verifier,
		OpenAIHandler:  openAIHandler.Routes(),
	})
	if err != nil {
		return nil, fmt.Errorf("web app: %w", err)
	}
	return server.NewRouter(server.Config{
		Version:    cfg.Version,
		Commit:     cfg.Commit,
		WebHandler: hostedWebHandler,
		DaemonHandler: daemonws.NewHandler(identity.DeviceAuthenticator{
			Verifier: verifier,
		}, b),
		OpenAIHandler: openAIHandler,
	}), nil
}

func newCredentialVerifier(cfg config.Config) (identity.CredentialVerifier, error) {
	verifier := identity.NewMemoryCredentialVerifier()
	if cfg.APIToken == "" && cfg.JWTSecret == "" && cfg.JWKSURL == "" {
		return nil, fmt.Errorf("api token, jwt secret, or jwks url is required")
	}
	hasStaticSeed := false
	if cfg.APIToken != "" {
		if err := verifier.AddAPIClient(cfg.APIToken, identity.APIClientClaims{
			Subject:   cfg.UserID,
			Audiences: []string{identity.AudienceKittyHome},
			Version:   identity.CredentialVersion1,
			Scopes:    []identity.Scope{identity.ScopeChatRelay, identity.ScopeModelsRead},
			UserID:    cfg.UserID,
			DeviceID:  cfg.DeviceID,
			AccountID: cfg.LocalAccountID,
		}); err != nil {
			return nil, fmt.Errorf("seed api client: %w", err)
		}
		hasStaticSeed = true
	}
	if cfg.DeviceToken != "" {
		if err := verifier.AddDevice(cfg.DeviceToken, identity.DeviceClaims{
			Subject:         "device:" + cfg.DeviceID,
			Audiences:       []string{identity.AudienceKittyHome},
			Version:         identity.CredentialVersion1,
			Scopes:          []identity.Scope{identity.ScopeDaemonConnect},
			UserID:          cfg.UserID,
			DeviceID:        cfg.DeviceID,
			LocalAccountIDs: []string{cfg.LocalAccountID},
		}); err != nil {
			return nil, fmt.Errorf("seed device: %w", err)
		}
		hasStaticSeed = true
	}
	if cfg.JWTSecret != "" || cfg.JWKSURL != "" {
		jwtVerifier, err := identity.NewJWTCredentialVerifier(identity.JWTVerifierConfig{
			Secret:  cfg.JWTSecret,
			JWKSURL: cfg.JWKSURL,
		})
		if err != nil {
			return nil, fmt.Errorf("jwt verifier: %w", err)
		}
		if hasStaticSeed {
			return identity.ChainCredentialVerifier{
				Verifiers: []identity.CredentialVerifier{jwtVerifier, verifier},
			}, nil
		}
		return jwtVerifier, nil
	}
	return verifier, nil
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
