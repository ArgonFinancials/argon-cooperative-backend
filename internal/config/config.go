package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
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
	// CSRFKey: ARGON_CSRF_KEY, hex, at least 32 bytes; required (2026-10-24).
	CSRFKey []byte
	// The sign-in code sender (sms.go): ARGON_SMS, termii or refusing, required
	// with an auth service; termii's three settings have no defaults.
	SMS                                     string
	TermiiURL, TermiiAPIKey, TermiiSenderID string
	// VFD's notifications (webhook.go in internal/api): ARGON_VFD_WEBHOOK,
	// hmac, unsigned or off, no default; hmac needs VFD_WEBHOOK_SECRET.
	VFDWebhook       string
	VFDWebhookSecret []byte
	// The enrolment code sender for officers (internal/email, 0034):
	// ARGON_EMAIL, ses or off, no default; ses needs EMAIL_FROM (the verified
	// sender address) and AWS_REGION. The credentials are the instance's role.
	Email                string
	EmailFrom, AWSRegion string
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
	// The CSRF key: required ALWAYS, auth service or not (2026-10-24). Bearer
	// sessions are served either way, and a cookie session must never be
	// checked against a missing key. No default: a key made at each start would
	// sign every page out on every restart. Rotating it refuses every live
	// page's token; each page recovers by reloading and re-reading /me.
	ck, err := hex.DecodeString(os.Getenv("ARGON_CSRF_KEY"))
	if err != nil || len(ck) < 32 {
		return c, fmt.Errorf("ARGON_CSRF_KEY must be set, at least 32 bytes as hex (openssl rand -hex 32)")
	}
	c.CSRFKey = ck
	if err := c.loadWebhook(); err != nil {
		return c, err
	}
	if err := c.loadEmail(); err != nil {
		return c, err
	}
	return c, c.loadSMS()
}

// loadWebhook: how VFD's notifications are taken, said and never inherited.
// No refusal repeats the secret.
func (c *Config) loadWebhook() error {
	c.VFDWebhook = os.Getenv("ARGON_VFD_WEBHOOK")
	switch c.VFDWebhook {
	case "off", "unsigned":
		return nil
	case "hmac":
		s := os.Getenv("VFD_WEBHOOK_SECRET")
		if len(s) < 32 {
			return fmt.Errorf("ARGON_VFD_WEBHOOK=hmac: VFD_WEBHOOK_SECRET must be set, at least 32 characters")
		}
		c.VFDWebhookSecret = []byte(s)
		return nil
	}
	return fmt.Errorf("ARGON_VFD_WEBHOOK must be set to hmac, unsigned or off (deploy/README.md, the wallet)")
}

// loadEmail: whether officers' codes can be emailed, said and never inherited.
// off: issuing by email answers 503 email_unavailable, and the slip works.
func (c *Config) loadEmail() error {
	c.Email = os.Getenv("ARGON_EMAIL")
	switch c.Email {
	case "off":
		return nil
	case "ses":
		c.EmailFrom, c.AWSRegion = os.Getenv("EMAIL_FROM"), os.Getenv("AWS_REGION")
		if !strings.Contains(c.EmailFrom, "@") || strings.ContainsAny(c.EmailFrom, " ,;<>") {
			return fmt.Errorf("ARGON_EMAIL=ses: EMAIL_FROM must be the one verified sender address")
		}
		if c.AWSRegion == "" {
			return fmt.Errorf("ARGON_EMAIL=ses: AWS_REGION must be set (the SES region the sender is verified in)")
		}
		return nil
	}
	return fmt.Errorf("ARGON_EMAIL must be set to ses or off (deploy/README.md, a new key on an existing instance)")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
