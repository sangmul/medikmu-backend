.PHONY: help up tunnel down logs run test lint fmt vet build migrate-up migrate-down migrate-new docker-build

DB_URL ?= postgres://medikmu:medikmu@localhost:5432/medikmu?sslmode=disable

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

up: ## Start API + Postgres + Redis (hot reload, auto-migrate)
	@test -f .env || cp .env.example .env
	docker compose up --build

tunnel: ## Start the stack plus an ngrok tunnel (needs NGROK_AUTHTOKEN, NGROK_DOMAIN in .env)
	@test -f .env || cp .env.example .env
	@grep -Eq '^NGROK_AUTHTOKEN=.+' .env || { echo "Set NGROK_AUTHTOKEN in .env (see docs/dev-ngrok.md)"; exit 1; }
	@grep -Eq '^NGROK_DOMAIN=.+' .env || { echo "Set NGROK_DOMAIN in .env (see docs/dev-ngrok.md)"; exit 1; }
	docker compose --profile tunnel up --build

down: ## Stop the local stack (and the tunnel, if running)
	docker compose --profile tunnel down

logs: ## Tail API logs
	docker compose logs -f api

run: ## Run the API on the host (needs a reachable Postgres)
	go run ./cmd/api

test: ## Run tests with the race detector
	go test -race ./...

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format code
	gofmt -w .

vet: ## go vet
	go vet ./...

build: ## Build the binary into ./bin
	CGO_ENABLED=0 go build -trimpath -o bin/api ./cmd/api

docker-build: ## Build the production image
	docker build -t medikmu-api:local .

migrate-up: ## Apply all migrations (needs golang-migrate CLI)
	migrate -path migrations -database "$(DB_URL)" up

migrate-down: ## Roll back one migration
	migrate -path migrations -database "$(DB_URL)" down 1

migrate-new: ## Create a migration: make migrate-new name=add_appointments
	migrate create -ext sql -dir migrations -seq $(name)
