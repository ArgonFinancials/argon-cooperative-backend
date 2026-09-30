.PHONY: up down db-setup migrate-up migrate-down verify-tier1 backup restore-drill run test tidy

ENVF ?= deploy/env/local.env

up:            ## start postgres
	docker compose --env-file $(ENVF) up -d --wait

down:
	docker compose --env-file $(ENVF) down

db-setup:      ## roles + database (idempotent)
	ARGON_ENV=$(ENVF) bash deploy/20-roles.sh

migrate-up:    ## apply migrations as argon_migrator, then grants
	ARGON_ENV=$(ENVF) bash deploy/40-migrate.sh up

migrate-down:  ## roll back one migration
	ARGON_ENV=$(ENVF) bash deploy/40-migrate.sh down 1

verify-tier1:  ## probe the Tier 1 constraints in a scratch database
	ARGON_ENV=$(ENVF) bash deploy/70-verify-tier1.sh

tier1-mutate:  ## 0002 acceptance tests against every deliberately broken schema
	ARGON_ENV=$(ENVF) bash test/tier1/mutate.sh

backup:
	ARGON_ENV=$(ENVF) bash deploy/50-backup.sh

restore-drill:
	ARGON_ENV=$(ENVF) bash deploy/60-restore.sh --drill

run:           ## API against the local container, env from .env
	set -a && . ./.env && set +a && go run ./cmd/api

test:
	go test ./...

tidy:
	go mod tidy
