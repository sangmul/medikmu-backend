.PHONY: help up tunnel down logs run test lint fmt vet build migrate-up migrate-down migrate-new docker-build

DB_URL ?= postgres://medikmu:medikmu@localhost:5432/medikmu?sslmode=disable

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# A new .env gets its own random JWT_SECRET: the placeholder in .env.example is public, so anyone could
# forge tokens against an API that uses it (and `make tunnel` makes the API public).
.env:
	cp .env.example .env
	@secret=$$(openssl rand -hex 32) && sed -i.bak "s|^JWT_SECRET=.*|JWT_SECRET=$$secret|" .env && rm -f .env.bak
	@echo "Created .env with a random JWT_SECRET"

up: .env ## Start API + Postgres + Redis (hot reload, auto-migrate)
	docker compose up --build

tunnel: .env ## Start the stack plus an ngrok tunnel (needs NGROK_AUTHTOKEN, NGROK_DOMAIN in .env)
	@! grep -Eqi '^JWT_SECRET=(dev-only|change-me)' .env || { echo "JWT_SECRET in .env is the public placeholder. Run: openssl rand -hex 32, and put it in .env"; exit 1; }
	@grep -Eq '^NGROK_AUTHTOKEN=.+' .env || { echo "Set NGROK_AUTHTOKEN in .env (see docs/dev-ngrok.md)"; exit 1; }
	@grep -Eq '^NGROK_DOMAIN=[^/:[:space:]]+$$' .env || { echo "Set NGROK_DOMAIN in .env to the bare domain, e.g. my-name.ngrok-free.app (no https://). See docs/dev-ngrok.md"; exit 1; }
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
