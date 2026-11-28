// Command vfd-proxy is the relay on the wallet platform's server that carries
// the cooperative's VFD calls (internal/vfdproxy: read its package comment
// first). It runs as its own service, under its own user, behind nginx, which
// admits 54.85.168.60 only and serves the relay's own name and certificate.
//
// THE COOPERATIVE NO LONGER HOLDS THE VFD TOKEN: it is in this service's own
// env file alone. Someone who takes over the cooperative's server gets five
// calls, from one address, and nothing portable.
//
// Its settings come from its own env file (root-only, read by systemd); it
// never reads the wallet's configuration:
//
//	VFD_BASE_URL       VFD's base URL (https), as the wallet uses it
//	VFD_ACCESS_TOKEN   VFD's token: never logged, never sent back
//	PROXY_KEYS         the signing keys, hex, 32 bytes or more each,
//	                   comma-separated, the current one first (two only
//	                   while rotating)
//	PROXY_LISTEN       where nginx reaches it (default 127.0.0.1:8095)
//	PROXY_LOG          the record, one line per call (default
//	                   /var/log/argon-vfd-proxy/requests.log), appended, 0600
package main

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/argon/backend/internal/vfdproxy"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("vfd-proxy will not start", "err", err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var keys [][]byte
	for _, k := range strings.Split(os.Getenv("PROXY_KEYS"), ",") {
		if k = strings.TrimSpace(k); k == "" {
			continue
		}
		b, err := hex.DecodeString(k)
		if err != nil {
			return fmt.Errorf("PROXY_KEYS: a key is not hex")
		}
		keys = append(keys, b)
	}
	listen := os.Getenv("PROXY_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8095"
	}
	logPath := os.Getenv("PROXY_LOG")
	if logPath == "" {
		logPath = "/var/log/argon-vfd-proxy/requests.log"
	}
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("the record %s cannot be opened: %w", logPath, err)
	}
	defer f.Close()
	h, err := vfdproxy.New(vfdproxy.Config{Upstream: os.Getenv("VFD_BASE_URL"), Token: os.Getenv("VFD_ACCESS_TOKEN"), Keys: keys, Log: f})
	if err != nil {
		return err
	}
	log.Info("vfd-proxy listening", "listen", listen, "keys", len(keys), "record", logPath)
	srv := &http.Server{Addr: listen, Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 70 * time.Second}
	return srv.ListenAndServe()
}
