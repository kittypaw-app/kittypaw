package config

import (
	"fmt"
	"os"
)

type Config struct {
	BindAddr       string
	APIToken       string
	DeviceToken    string
	JWTSecret      string
	JWKSURL        string
	UserID         string
	DeviceID       string
	LocalAccountID string
	PublicBaseURL  string
	APIAuthBaseURL string
	Version        string
	Commit         string
}

func Load() (Config, error) {
	cfg := Config{
		BindAddr:       bindAddr(),
		APIToken:       os.Getenv("KITTYHOME_API_TOKEN"),
		DeviceToken:    os.Getenv("KITTYHOME_DEVICE_TOKEN"),
		JWTSecret:      env("KITTYHOME_JWT_SECRET", os.Getenv("JWT_SECRET")),
		JWKSURL:        os.Getenv("KITTYHOME_JWKS_URL"),
		UserID:         os.Getenv("KITTYHOME_USER_ID"),
		DeviceID:       os.Getenv("KITTYHOME_DEVICE_ID"),
		LocalAccountID: os.Getenv("KITTYHOME_LOCAL_ACCOUNT_ID"),
		PublicBaseURL:  env("KITTYHOME_PUBLIC_BASE_URL", "https://home.kittypaw.app"),
		APIAuthBaseURL: env("KITTYHOME_API_AUTH_BASE_URL", "https://portal.kittypaw.app/auth"),
		Version:        env("KITTYHOME_VERSION", "dev"),
		Commit:         os.Getenv("KITTYHOME_COMMIT"),
	}

	hasJWTVerifier := cfg.JWTSecret != "" || cfg.JWKSURL != ""
	required := map[string]string{}
	if !hasJWTVerifier {
		required["KITTYHOME_API_TOKEN"] = cfg.APIToken
		required["KITTYHOME_DEVICE_TOKEN"] = cfg.DeviceToken
	}
	if !hasJWTVerifier || cfg.APIToken != "" || cfg.DeviceToken != "" {
		required["KITTYHOME_USER_ID"] = cfg.UserID
		required["KITTYHOME_DEVICE_ID"] = cfg.DeviceID
		required["KITTYHOME_LOCAL_ACCOUNT_ID"] = cfg.LocalAccountID
	}
	for name, value := range required {
		if value == "" {
			return Config{}, fmt.Errorf("%s is required", name)
		}
	}
	if cfg.JWTSecret != "" && len(cfg.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("KITTYHOME_JWT_SECRET must be at least 32 characters")
	}
	return cfg, nil
}

func bindAddr() string {
	if value := os.Getenv("KITTYHOME_BIND_ADDR"); value != "" {
		return value
	}
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
