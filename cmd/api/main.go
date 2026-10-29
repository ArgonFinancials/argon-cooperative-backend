// Command api is the Argon backend.
//
// Read /LEDGER_CONTRACT.md before changing anything in internal/ledger or
// migrations/. The constraints there are Tier 1: a human writes the failing
// acceptance test before any of them is implemented.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
	"github.com/argon/backend/internal/config"
	"github.com/argon/backend/internal/db"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Every route comes from api/openapi.yaml through the generated router; a
	// path that is not in the spec does not exist.
	deps := api.Deps{DB: pool, Log: log, AllowedOrigins: api.ParseOrigins(cfg.AllowedOrigins), CSRFKey: cfg.CSRFKey}
	if len(deps.AllowedOrigins) == 0 {
		log.Info("no browser origin may call this API (ARGON_ALLOWED_ORIGINS is empty: closed by default)")
	} else {
		log.Info("browser origins allowed", "origins", deps.AllowedOrigins)
	}
	if cfg.AuthDatabaseURL == "" {
		// Deliberate: sign-in and the PIN step-up answer 503 until the auth
		// service has its own argon_auth connection (AUTH_DATABASE_URL).
		log.Warn("auth service not configured: sign-in and the PIN step-up answer 503, and imports cannot be authorised",
			"needs", "AUTH_DATABASE_URL and AUTH_CODE_KEY")
	} else {
		authPool, err := db.Open(ctx, cfg.AuthDatabaseURL) // as argon_auth: the only role that writes auth state
		if err != nil {
			log.Error("auth database", "err", err)
			os.Exit(1)
		}
		defer authPool.Close()
		// The code sender is chosen, never defaulted (ARGON_SMS). refusing: every
		// send is refused loudly and a new device is routed to an officer.
		// Known devices and the step-up need no SMS.
		var sms auth.SMS = auth.UnavailableSMS{Log: log}
		if cfg.SMS == "termii" {
			if sms, err = auth.NewTermii(auth.TermiiConfig{URL: cfg.TermiiURL, APIKey: cfg.TermiiAPIKey, SenderID: cfg.TermiiSenderID, Log: log}); err != nil {
				log.Error("sms sender", "err", err)
				os.Exit(1)
			}
		}
		svc, err := auth.New(auth.Deps{
			DB:      authPool,
			SMS:     sms,
			Notify:  auth.LogNotifier{Log: log},
			CodeKey: cfg.AuthCodeKey,
			Argon:   auth.Argon2{MemoryKiB: 19 * 1024, Time: 2, Threads: 1},
		})
		if err != nil {
			log.Error("auth service", "err", err)
			os.Exit(1)
		}
		if cfg.SMS == "termii" {
			log.Info("sign-in codes are sent through Termii", "sender_id", cfg.TermiiSenderID)
		} else {
			log.Warn("sign-in runs with the REFUSING SMS sender (ARGON_SMS=refusing): new devices are routed to an officer")
		}
		deps.Auth = svc
	}
	h, err := api.NewServer(deps)
	if err != nil {
		log.Error("api", "err", err)
		os.Exit(1)
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Mount("/", h)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("listening", "port", cfg.Port, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
