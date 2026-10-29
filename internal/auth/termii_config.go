package auth

import (
	"log/slog"
	"time"
)

// TermiiConfig: what the Termii sender needs (the Termii sender, piece 1,
// approved 2026-10-28). Every field is required; none has a working default.
type TermiiConfig struct {
	URL      string        // TERMII_URL: the send endpoint
	APIKey   string        // TERMII_API_KEY: which account sends is decided by the key alone
	SenderID string        // TERMII_SENDER_ID: the name the phone shows; configured, never written in code
	Timeout  time.Duration // one call per code; zero means the 10-second default
	Log      *slog.Logger  // nil: slog.Default()
}
