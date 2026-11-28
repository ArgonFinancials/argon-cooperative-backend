package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

// Wallet: the wallet worker's configuration (stage 3, 2026-11-10). Its own
// process and its own connection, as argon_wallet: the only role that writes
// what VFD said (0028). Nothing here has a default that works.
type Wallet struct {
	DatabaseURL string // WALLET_DATABASE_URL, as argon_wallet
	VFDBaseURL  string // VFD_BASE_URL, https
	VFDToken    string // VFD_ACCESS_TOKEN: calling VFD directly
	// VFDProxyKey: VFD_PROXY_KEY (hex, 32 bytes or more): calling VFD through
	// the relay on the wallet's server instead (cmd/vfd-proxy, 2026-11-28),
	// with NO token here. Exactly one of the two.
	VFDProxyKey []byte
	Tick        time.Duration
}

// LoadWallet reads the worker's settings. ARGON_VFD is said, never inherited:
// "vfd" runs the worker; "off" refuses to start it, saying so (until VFD's
// credentials exist the last signature still places the hold, and the
// transfer waits); anything else is a refusal. No refusal repeats a value.
func LoadWallet() (Wallet, error) {
	var w Wallet
	switch os.Getenv("ARGON_VFD") {
	case "vfd":
	case "off":
		return w, fmt.Errorf("ARGON_VFD=off: the wallet worker does not run, and no payout is sent; holds the last signature placed wait (deploy/README.md, the wallet)")
	default:
		return w, fmt.Errorf("ARGON_VFD must be set to vfd or off (deploy/README.md, the wallet)")
	}
	w.DatabaseURL = os.Getenv("WALLET_DATABASE_URL")
	w.VFDBaseURL = os.Getenv("VFD_BASE_URL")
	w.VFDToken = os.Getenv("VFD_ACCESS_TOKEN")
	switch {
	case w.DatabaseURL == "" || !strings.Contains(w.DatabaseURL, "argon_wallet"):
		return w, fmt.Errorf("WALLET_DATABASE_URL must be set, as argon_wallet (the only role that writes what VFD said)")
	case !strings.HasPrefix(w.VFDBaseURL, "https://"):
		return w, fmt.Errorf("VFD_BASE_URL must be set, and https (the token travels in a header)")
	}
	key := os.Getenv("VFD_PROXY_KEY")
	switch {
	case w.VFDToken == "" && key == "":
		return w, fmt.Errorf("VFD_PROXY_KEY (through the relay) or VFD_ACCESS_TOKEN (direct) must be set")
	case w.VFDToken != "" && key != "":
		return w, fmt.Errorf("VFD_PROXY_KEY and VFD_ACCESS_TOKEN are both set: through the relay, this server holds no token (deploy/README.md, the VFD relay)")
	case key != "":
		b, err := hex.DecodeString(key)
		if err != nil || len(b) < 32 {
			return w, fmt.Errorf("VFD_PROXY_KEY must be at least 32 bytes as hex (openssl rand -hex 32)")
		}
		w.VFDProxyKey = b
	}
	w.Tick = 30 * time.Second
	if s := os.Getenv("WALLET_TICK"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < 5*time.Second {
			return w, fmt.Errorf("WALLET_TICK must be a duration of at least 5s")
		}
		w.Tick = d
	}
	return w, nil
}
