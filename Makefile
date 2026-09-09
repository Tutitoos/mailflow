.PHONY: check integration e2e acceptance-images release-acceptance dev api worker compose-config

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

integration:
	./scripts/test-integration.sh

e2e:
	bun run --cwd apps/web test:e2e

acceptance-images:
	./scripts/build-acceptance-images.sh

release-acceptance: integration e2e acceptance-images
	./scripts/verify-container-images.sh health

dev:
	bun run dev

api:
	cd services/api && go run ./cmd/api

worker:
	cd services/api && go run ./cmd/worker

compose-config:
	docker compose --env-file deploy/.env.example -f deploy/compose.yml config --quiet
