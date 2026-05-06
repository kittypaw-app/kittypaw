package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/kittypaw-app/kittyportal/internal/admin"
	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/config"
	"github.com/kittypaw-app/kittyportal/internal/connect"
	"github.com/kittypaw-app/kittyportal/internal/connectadmin"
	"github.com/kittypaw-app/kittyportal/internal/janitor"
	"github.com/kittypaw-app/kittyportal/internal/model"
	"github.com/kittypaw-app/kittyportal/internal/ratelimit"
)

const shutdownGrace = 30 * time.Second

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	initLogging()

	if err := run(); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func initLogging() {
	raw := strings.ToLower(os.Getenv("LOG_LEVEL"))
	level := slog.LevelInfo
	known := true
	switch raw {
	case "", "info":
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		known = false
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	if !known {
		slog.Warn("unknown LOG_LEVEL, falling back to info", "value", raw)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	pool, err := model.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()

	userStore := model.NewUserStore(pool)
	refreshStore := model.NewRefreshTokenStore(pool)
	deviceStore := model.NewDeviceStore(pool)
	connectAdminStore := connectadmin.NewStore(pool)
	connectRegistry := connectadmin.DefaultProviderRegistry(connectadmin.ProviderRegistryConfig{
		GmailConfigured: cfg.ConnectGoogleClientID != "" && cfg.ConnectGoogleClientSecret != "",
		XConfigured:     cfg.ConnectXClientID != "" && cfg.ConnectXClientSecret != "",
	})
	if err := connectAdminStore.EnsureDefaultPolicies(ctx, connectRegistry); err != nil {
		return fmt.Errorf("seed connect policies: %w", err)
	}

	router, cleanup := NewRouter(cfg, userStore, refreshStore, deviceStore, connectAdminStore)
	defer cleanup()

	go janitor.New(deviceStore, refreshStore, janitor.DefaultPolicy, nil).Run(ctx)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", listenAddr(cfg))
		if err := serveHTTP(srv, cfg.UnixSocket); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining", "grace", shutdownGrace)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func listenAddr(cfg *config.Config) string {
	if cfg != nil && cfg.UnixSocket != "" {
		return "unix:" + cfg.UnixSocket
	}
	if cfg == nil {
		return ""
	}
	return ":" + cfg.Port
}

func serveHTTP(srv *http.Server, unixSocket string) error {
	if unixSocket == "" {
		return srv.ListenAndServe()
	}
	if err := os.MkdirAll(filepath.Dir(unixSocket), 0o755); err != nil {
		return err
	}
	if err := os.Remove(unixSocket); err != nil && !os.IsNotExist(err) {
		return err
	}
	ln, err := net.Listen("unix", unixSocket)
	if err != nil {
		return err
	}
	defer os.Remove(unixSocket)
	if err := os.Chmod(unixSocket, 0o666); err != nil {
		ln.Close()
		return err
	}
	return srv.Serve(ln)
}

// NewRouter builds the portal router and returns it with a cleanup hook
// for the in-memory state stores and rate limiter.
func NewRouter(cfg *config.Config, userStore model.UserStore, refreshStore model.RefreshTokenStore, deviceStore model.DeviceStore, connectAdminStore connectadmin.Store) (*chi.Mux, func()) {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		ExposedHeaders:   []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "Retry-After", "Warning"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	jwksProvider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)
	authMW := auth.Middleware(jwksProvider, auth.AudienceAPI, userStore)
	limiter := ratelimit.New()

	states := auth.NewStateStore()
	webCodes := auth.NewWebCodeStore()
	connectCodes := connect.NewCodeStore(connect.CodeStoreOptions{})
	oauthHandler := &auth.OAuthHandler{
		UserStore:         userStore,
		RefreshTokenStore: refreshStore,
		DeviceStore:       deviceStore,
		WebCodeStore:      webCodes,
		StateStore:        states,
		JWTPrivateKey:     cfg.JWTPrivateKey,
		JWTKID:            cfg.JWTKID,
		HTTPClient:        &http.Client{Timeout: 10 * time.Second},
		GoogleAuthURL:     cfg.GoogleAuthURL,
		GoogleTokenURL:    cfg.GoogleTokenURL,
		GoogleUserInfoURL: cfg.GoogleUserInfoURL,
	}

	cliCodes := auth.NewCLICodeStore()

	googleCfg := auth.GoogleConfig{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		RedirectURL:  cfg.BaseURL + "/auth/google/callback",
	}
	githubCfg := auth.GitHubConfig{
		ClientID:     cfg.GitHubClientID,
		ClientSecret: cfg.GitHubClientSecret,
		RedirectURL:  cfg.BaseURL + "/auth/github/callback",
	}

	discovery := map[string]string{
		"api_base_url":        cfg.APIBaseURL,
		"auth_base_url":       strings.TrimRight(cfg.BaseURL, "/") + "/auth",
		"skills_registry_url": cfg.SkillsRegistryURL,
	}
	if cfg.ConnectBaseURL != "" {
		discovery["connect_base_url"] = cfg.ConnectBaseURL
	}
	if cfg.KakaoRelayURL != "" {
		discovery["kakao_relay_url"] = cfg.KakaoRelayURL
	}
	if cfg.ChatRelayURL != "" {
		discovery["chat_relay_url"] = cfg.ChatRelayURL
	}

	identityOnly := hostBoundaryMiddleware(cfg.BaseURL, cfg.APIBaseURL, cfg.BaseURL)
	portalOnly := hostOnlyMiddleware(cfg.BaseURL)
	connectOnly := hostOnlyMiddleware(cfg.ConnectBaseURL)
	connectRegistry := connectadmin.DefaultProviderRegistry(connectadmin.ProviderRegistryConfig{
		GmailConfigured: cfg.ConnectGoogleClientID != "" && cfg.ConnectGoogleClientSecret != "",
		XConfigured:     cfg.ConnectXClientID != "" && cfg.ConnectXClientSecret != "",
	})

	r.Get("/health", handleHealth)

	r.Get("/", handleHostRoot(cfg))

	if cfg.ConnectBaseURL != "" {
		connectHandler := connect.NewHandler(
			connect.NewGmailProvider(connect.GmailConfig{
				ClientID:     cfg.ConnectGoogleClientID,
				ClientSecret: cfg.ConnectGoogleClientSecret,
				BaseURL:      cfg.ConnectBaseURL,
				AuthURL:      cfg.ConnectGoogleAuthURL,
				TokenURL:     cfg.ConnectGoogleTokenURL,
				UserInfoURL:  cfg.ConnectGoogleUserInfoURL,
			}, &http.Client{Timeout: 10 * time.Second}),
			connect.NewXProvider(connect.XConfig{
				ClientID:     cfg.ConnectXClientID,
				ClientSecret: cfg.ConnectXClientSecret,
				BaseURL:      cfg.ConnectBaseURL,
				AuthURL:      cfg.ConnectXAuthURL,
				TokenURL:     cfg.ConnectXTokenURL,
				UserInfoURL:  cfg.ConnectXUserInfoURL,
			}, &http.Client{Timeout: 10 * time.Second}),
			states,
			connectCodes,
		)
		r.Group(func(r chi.Router) {
			r.Use(connectOnly)
			r.Get("/connect", handleConnectHome(cfg))
			r.Get("/connect/", handleConnectHome(cfg))
			r.Get("/connect/gmail/login", connectHandler.HandleGmailLogin())
			r.Get("/connect/gmail/callback", connectHandler.HandleGmailCallback())
			r.Get("/connect/x/login", connectHandler.HandleXLogin())
			r.Get("/connect/x/callback", connectHandler.HandleXCallback())
			r.Post("/connect/cli/exchange", connectHandler.HandleCLIExchange())
			r.Post("/connect/gmail/refresh", connectHandler.HandleGmailRefresh())
			r.Post("/connect/x/refresh", connectHandler.HandleXRefresh())
		})
	}

	if connectAdminStore != nil {
		connectAdminHandler := connectadmin.NewHandler(connectadmin.HandlerOptions{
			Registry: connectRegistry,
			Store:    connectAdminStore,
		})
		r.Group(func(r chi.Router) {
			r.Use(portalOnly)
			r.Use(authMW)
			r.Use(admin.Middleware(cfg.PortalAdminEmails))
			r.Get("/admin/connect", connectAdminHandler.HandleHome())
			r.Get("/admin/connect/", connectAdminHandler.HandleHome())
		})
	}

	r.Group(func(r chi.Router) {
		r.Use(identityOnly)
		r.Use(ratelimit.Middleware(limiter, "refresh"))
		r.Post("/auth/devices/refresh", oauthHandler.HandleDeviceRefresh())
	})

	r.Group(func(r chi.Router) {
		r.Use(identityOnly)
		r.Use(ratelimit.Middleware(limiter, "web"))
		r.Post("/auth/web/exchange", oauthHandler.HandleWebExchange())
	})

	r.Group(func(r chi.Router) {
		r.Use(identityOnly)
		r.Use(authMW)
		r.Use(ratelimit.Middleware(limiter))

		r.Get("/.well-known/jwks.json", auth.HandleJWKS(jwksProvider))
		r.Get("/discovery", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(discovery); err != nil {
				slog.Error("encode discovery", "err", err)
			}
		})

		cliCfg := auth.CLILoginConfig{
			GoogleCfg: googleCfg,
			CodeStore: cliCodes,
			BaseURL:   cfg.BaseURL,
		}
		webCfg := auth.WebLoginConfig{
			GoogleCfg:            googleCfg,
			CodeStore:            webCodes,
			RedirectURIAllowlist: cfg.WebRedirectURIAllowlist,
		}
		r.Route("/auth", func(r chi.Router) {
			r.Get("/google", oauthHandler.HandleGoogleLogin(googleCfg))
			r.Get("/google/callback", oauthHandler.HandleGoogleCallback(googleCfg))
			r.Get("/github", oauthHandler.HandleGitHubLogin(githubCfg))
			r.Get("/github/callback", oauthHandler.HandleGitHubCallback(githubCfg))
			r.Post("/token/refresh", oauthHandler.HandleTokenRefresh())
			r.Get("/me", auth.HandleMe)

			r.Get("/cli/{provider}", oauthHandler.HandleCLILogin(cliCfg))
			r.Get("/cli/callback", oauthHandler.HandleCLICallback(cliCfg))
			r.Post("/cli/exchange", oauthHandler.HandleCLIExchange(cliCfg))

			r.Get("/web/google", oauthHandler.HandleWebGoogleLogin(webCfg))

			r.Post("/devices/pair", oauthHandler.HandlePair())
			r.Get("/devices", oauthHandler.HandleDevicesList())
			r.Delete("/devices/{id}", oauthHandler.HandleDeviceDelete())
		})
	})

	cleanup := func() {
		states.Close()
		cliCodes.Close()
		webCodes.Close()
		limiter.Close()
	}
	return r, cleanup
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "healthy",
		"version": version,
		"commit":  commit,
	})
}

func hostBoundaryMiddleware(identityBaseURL, resourceBaseURL, allowedBaseURL string) func(http.Handler) http.Handler {
	identityHost := canonicalURLHost(identityBaseURL)
	resourceHost := canonicalURLHost(resourceBaseURL)
	allowedHost := canonicalURLHost(allowedBaseURL)
	splitHosts := identityHost != "" && resourceHost != "" && identityHost != resourceHost
	if !splitHosts || allowedHost == "" {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestHost := canonicalHost(r.Host)
			if requestHost == allowedHost || isLocalRequestHost(requestHost) {
				next.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		})
	}
}

func hostOnlyMiddleware(allowedBaseURL string) func(http.Handler) http.Handler {
	allowedHost := canonicalURLHost(allowedBaseURL)
	if allowedHost == "" {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestHost := canonicalHost(r.Host)
			if requestHost == allowedHost || isLocalRequestHost(requestHost) || isLocalRequestHost(allowedHost) {
				next.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		})
	}
}

func canonicalURLHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return canonicalHost(u.Host)
}

func canonicalHost(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		hostport = host
	}
	return strings.ToLower(strings.Trim(hostport, "[]"))
}

func isLocalRequestHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
