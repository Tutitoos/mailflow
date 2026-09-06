.PHONY: check e2e dev api worker compose-config

check:
	bun run lint
	bun run typecheck
	bun run test
	bun run build
	cd services/api && go tool sqlc generate
	git diff --exit-code -- services/api/internal/platform/database/dbgen packages/api-client/src/generated
	cd services/api && go test -race ./... && go vet ./...
	cd apps/desktop/src-tauri && cargo check
	./scripts/repos-lock.sh validate

e2e:
	bun run --cwd apps/web test:e2e

dev:
	bun run dev

api:
	cd services/api && go run ./cmd/api

worker:
	cd services/api && go run ./cmd/worker

compose-config:
	docker compose --env-file deploy/.env.example -f deploy/compose.yml config --quiet
