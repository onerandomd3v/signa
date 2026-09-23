COMPOSE := docker compose -f deploy/docker-compose.yml
SIGNA_DATABASE_URL ?= postgres://signa:signa_local@localhost:5432/signa?sslmode=disable

.PHONY: infra-up infra-down infra-status migrate-up db-ping redis-ping

infra-up:
	$(COMPOSE) up -d

infra-down:
	$(COMPOSE) down

infra-status:
	$(COMPOSE) ps

migrate-up:
	go run github.com/pressly/goose/v3/cmd/goose@v3.27.0 -dir migrations postgres "$(SIGNA_DATABASE_URL)" up

db-ping:
	go test -count=1 -tags=integration ./internal/platform/postgres -run TestPostgresPing -v

redis-ping:
	go test -count=1 -tags=integration ./internal/platform/redis -run TestRedisPing -v
