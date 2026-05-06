package smoke

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultRemoteSmokeTimeout = 15 * time.Second

type RemoteConfig struct {
	BaseURL        string
	UserToken      string
	DeviceToken    string
	DeviceID       string
	LocalAccountID string
	UserID         string
	Timeout        time.Duration
}

func LoadRemoteConfig() (RemoteConfig, error) {
	cfg := RemoteConfig{
		BaseURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("HOME_BASE_URL")), "/"),
		UserToken:      strings.TrimSpace(os.Getenv("HOME_USER_TOKEN")),
		DeviceToken:    strings.TrimSpace(os.Getenv("HOME_DEVICE_TOKEN")),
		DeviceID:       strings.TrimSpace(os.Getenv("HOME_DEVICE_ID")),
		LocalAccountID: strings.TrimSpace(os.Getenv("HOME_LOCAL_ACCOUNT_ID")),
		UserID:         strings.TrimSpace(os.Getenv("HOME_SMOKE_USER_ID")),
		Timeout:        defaultRemoteSmokeTimeout,
	}
	for name, value := range map[string]string{
		"HOME_BASE_URL":         cfg.BaseURL,
		"HOME_USER_TOKEN":       cfg.UserToken,
		"HOME_DEVICE_TOKEN":     cfg.DeviceToken,
		"HOME_DEVICE_ID":        cfg.DeviceID,
		"HOME_LOCAL_ACCOUNT_ID": cfg.LocalAccountID,
	} {
		if value == "" {
			return RemoteConfig{}, fmt.Errorf("%s is required", name)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("HOME_SMOKE_TIMEOUT")); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil {
			return RemoteConfig{}, fmt.Errorf("HOME_SMOKE_TIMEOUT: %w", err)
		}
		cfg.Timeout = timeout
	}
	if _, err := remoteWebSocketURL(cfg.BaseURL); err != nil {
		return RemoteConfig{}, fmt.Errorf("HOME_BASE_URL: %w", err)
	}
	return cfg, nil
}

func remoteWebSocketURL(baseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	basePath := strings.TrimRight(u.Path, "/")
	if basePath == "" {
		u.Path = "/daemon/connect"
	} else {
		u.Path = basePath + "/daemon/connect"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
