package config

import (
	"fmt"
	"os"
)

type Config struct {
	BindAddr           string
	APIToken           string
	DeviceToken        string
	JWTSecret          string
	JWKSURL            string
	UserID             string
	DeviceID           string
	LocalAccountID     string
	PublicBaseURL      string
	APIAuthBaseURL     string
	KittyPawStableFile string
	Version            string
	Commit             string
}

func Load() (Config, error) {
	cfg := Config{
		BindAddr:           bindAddr(),
		APIToken:           os.Getenv("KITTYSPACE_API_TOKEN"),
		DeviceToken:        os.Getenv("KITTYSPACE_DEVICE_TOKEN"),
		JWTSecret:          env("KITTYSPACE_JWT_SECRET", os.Getenv("JWT_SECRET")),
		JWKSURL:            os.Getenv("KITTYSPACE_JWKS_URL"),
		UserID:             os.Getenv("KITTYSPACE_USER_ID"),
		DeviceID:           os.Getenv("KITTYSPACE_DEVICE_ID"),
		LocalAccountID:     os.Getenv("KITTYSPACE_LOCAL_ACCOUNT_ID"),
		PublicBaseURL:      env("KITTYSPACE_PUBLIC_BASE_URL", "https://space.kittypaw.app"),
		APIAuthBaseURL:     env("KITTYSPACE_API_AUTH_BASE_URL", "https://portal.kittypaw.app/auth"),
		KittyPawStableFile: env("KITTYSPACE_KITTYPAW_STABLE_FILE", "/home/jinto/kittyspace/public/kittypaw/stable.json"),
		Version:            env("KITTYSPACE_VERSION", "dev"),
		Commit:             os.Getenv("KITTYSPACE_COMMIT"),
	}

	hasJWTVerifier := cfg.JWTSecret != "" || cfg.JWKSURL != ""
	required := map[string]string{}
	if !hasJWTVerifier {
		required["KITTYSPACE_API_TOKEN"] = cfg.APIToken
		required["KITTYSPACE_DEVICE_TOKEN"] = cfg.DeviceToken
	}
	if !hasJWTVerifier || cfg.APIToken != "" || cfg.DeviceToken != "" {
		required["KITTYSPACE_USER_ID"] = cfg.UserID
		required["KITTYSPACE_DEVICE_ID"] = cfg.DeviceID
		required["KITTYSPACE_LOCAL_ACCOUNT_ID"] = cfg.LocalAccountID
	}
	for name, value := range required {
		if value == "" {
			return Config{}, fmt.Errorf("%s is required", name)
		}
	}
	if cfg.JWTSecret != "" && len(cfg.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("KITTYSPACE_JWT_SECRET must be at least 32 characters")
	}
	return cfg, nil
}

func bindAddr() string {
	if value := os.Getenv("KITTYSPACE_BIND_ADDR"); value != "" {
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
