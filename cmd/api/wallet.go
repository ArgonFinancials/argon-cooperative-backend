package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/argon/backend/internal/config"
	"github.com/argon/backend/internal/db"
	"github.com/argon/backend/internal/vfd"
	"github.com/argon/backend/internal/wallet"
)

// runWallet: one tick at a time, until told to stop. A tick that fails is
// logged with its error and the next one runs: every record a tick writes is
// its own transaction, so a failure part-way leaves what was written true
// and the rest for the next tick.
func runWallet(log *slog.Logger) int {
	cfg, err := config.LoadWallet()
	if err != nil {
		log.Error("wallet configuration", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("wallet database", "err", err)
		return 1
	}
	defer pool.Close()
	client, err := vfd.New(vfd.Config{BaseURL: cfg.VFDBaseURL, AccessToken: cfg.VFDToken, Log: log})
	if err != nil {
		log.Error("vfd client", "err", err)
		return 1
	}
	w, err := wallet.New(wallet.Config{DB: pool, VFD: client, Log: log})
	if err != nil {
		log.Error("wallet", "err", err)
		return 1
	}
	log.Info("wallet worker running", "tick", cfg.Tick.String())
	t := time.NewTicker(cfg.Tick)
	defer t.Stop()
	for {
		r, err := w.Tick(ctx)
		if err != nil {
			log.Error("wallet tick failed; the next tick tries again", "err", err)
		} else if r != (wallet.Round{}) {
			log.Info("wallet tick", "sent", r.Sent, "asked", r.Asked, "paid", r.Paid, "failed", r.Failed, "waiting", r.Waiting)
		}
		select {
		case <-ctx.Done():
			log.Info("wallet worker stopping")
			return 0
		case <-t.C:
		}
	}
}
