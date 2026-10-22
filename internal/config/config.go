package config

import (
	"encoding/hex"
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL string
	Port        string
	Env         string
	// The auth service (sign-in, the PIN step-up) runs on its OWN connection,
	// as argon_auth: the only role that writes auth state. Both or neither:
	// with neither, sign-in and the step-up answer a deliberate 503.
	AuthDatabaseURL string
	AuthCodeKey     []byte // HMAC key for sign-in code digests; AUTH_CODE_KEY, hex, at least 32 bytes
	// The browser origins that may call the API (ARGON_ALLOWED_ORIGINS,
	// comma-separated, exact). Empty: no browser origin (closed by default).
	AllowedOrigins string
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Port:        envOr("PORT", "8080"),
		Env:         envOr("ENV", "development"),
		// No default, deliberately: production's value is a decision someone
		// makes, not one they inherit.
		AllowedOrigins: os.Getenv("ARGON_ALLOWED_ORIGINS"),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is not set — see .env.example")
	}
	c.AuthDatabaseURL = os.Getenv("AUTH_DATABASE_URL")
	key := os.Getenv("AUTH_CODE_KEY")
	switch {
	case c.AuthDatabaseURL == "" && key == "":
		// not configured: deliberate, and logged at startup
	case c.AuthDatabaseURL == "" || key == "":
		return c, fmt.Errorf("AUTH_DATABASE_URL and AUTH_CODE_KEY are set together or not at all: half an auth service is a misconfiguration")
	default:
		b, err := hex.DecodeString(key)
		if err != nil || len(b) < 32 {
			return c, fmt.Errorf("AUTH_CODE_KEY must be at least 32 bytes as hex (openssl rand -hex 32)")
		}
		c.AuthCodeKey = b
	}
	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
