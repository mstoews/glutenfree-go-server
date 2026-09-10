# DB_URL is the local Docker Postgres for development. Override on the command
# line for ad-hoc environments: `make migrateup DB_URL=postgresql://...`.
DB_URL ?= postgresql://root:secret@localhost:5432/glutenfree?sslmode=disable

# -B gobuildid forces a build-id -> LC_UUID load command. Go 1.22's internal
# linker omits LC_UUID, which macOS 15+/Darwin 25 dyld now requires; without it
# the binary is SIGKILLed at launch with no output. Harmless on newer Go
# toolchains; drop once this project is on Go 1.23+.
LDFLAGS := -B gobuildid

# CGO_ENABLED=0: this is a pure-Go service (pgx, gin — no cgo). A static binary
# keeps the internal linker path where -B gobuildid actually emits LC_UUID; the
# macOS-default CGO_ENABLED=1 links differently and drops it. Also simplifies
# container builds.
export CGO_ENABLED=0

postgres:
	docker run --name glutenfree-pg -p 5432:5432 \
		-e POSTGRES_USER=root -e POSTGRES_PASSWORD=secret \
		-d postgres:16-alpine

createdb:
	docker exec -it glutenfree-pg createdb --username=root --owner=root glutenfree

dropdb:
	docker exec -it glutenfree-pg dropdb glutenfree

# Deploy to Cloud Run from source (uses the Dockerfile). Secrets come from
# Secret Manager (db-source, token-symmetric-key); HTTP_SERVER_ADDRESS is left
# unset so the app honors Cloud Run's injected PORT (8080). app.env is excluded
# from the upload by .gcloudignore.
# GCP_PROJECT pins the deploy to the project that owns the db-source /
# token-symmetric-key secrets. Without it, `gcloud run deploy` targets the
# active gcloud project, which may not have those secrets (a common footgun).
GCP_PROJECT ?= gurufuri

# Every secret named below must already have a version, or Cloud Run rejects
# the deploy. Seed a new one with:
#   printf '%s' '<value>' | gcloud secrets versions add <name> --project $(GCP_PROJECT) --data-file=-
# MAIL_FROM must be on a domain verified in Resend, and ADMIN_PORTAL_URL is
# the origin emailed reset links are built from.
# BRAVE_SEARCH_API_KEY powers automatic restaurant discovery; without it
# /internal/discovery/run rejects search-only requests (pasted URLs still work).
deploy:
	gcloud run deploy glutenfree-go-server --source . \
		--project $(GCP_PROJECT) \
		--region asia-east1 --platform managed \
		--allow-unauthenticated \
		--min-instances=0 \
		--set-secrets=DB_SOURCE=db-source:latest,TOKEN_SYMMETRIC_KEY=token-symmetric-key:latest,RESEND_API_KEY=resend-api-key:latest,BRAVE_SEARCH_API_KEY=brave-search-api-key:latest \
		--set-env-vars="ENVIRONMENT=production,ACCESS_TOKEN_DURATION=15m,REFRESH_TOKEN_DURATION=720h,ALLOWED_ORIGINS=*,APPLE_BUNDLE_ID=com.glutenfree.app,IMAGE_BUCKET=gurufuri-images,MAIL_FROM=no-reply@gurufuri-jp.com,MAIL_FROM_NAME=Gurufuri Admin,ADMIN_PORTAL_URL=https://gurufuri-admin.web.app"

migrateup:
	migrate -path db/migration -database "$(DB_URL)" -verbose up

migrateup1:
	migrate -path db/migration -database "$(DB_URL)" -verbose up 1

migratedown:
	migrate -path db/migration -database "$(DB_URL)" -verbose down

migratedown1:
	migrate -path db/migration -database "$(DB_URL)" -verbose down 1

new_migration:
	migrate create -ext sql -dir db/migration -seq $(name)

sqlc:
	sqlc generate

server:
	go run -ldflags="$(LDFLAGS)" ./cmd/main.go

build:
	go build -ldflags="$(LDFLAGS)" -o bin/server ./cmd/main.go

test:
	go test -v -cover ./...

# TEST_DB_URL is the throwaway Postgres used by `test-integration` (own port so
# it never collides with the :5432 dev container). Override for ad-hoc runs.
TEST_DB_URL ?= postgresql://root:secret@localhost:5433/glutenfree_test?sslmode=disable

# test-integration spins a disposable Postgres on :5433, migrates it, runs the
# `integration`-tagged tests (real sqlc queries + FK cascade) against it, then
# tears the container down whether or not the tests pass. Needs Docker + migrate.
test-integration:
	docker rm -f glutenfree-pg-test >/dev/null 2>&1 || true
	docker run --name glutenfree-pg-test -p 5433:5432 \
		-e POSTGRES_USER=root -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=glutenfree_test \
		-d postgres:16-alpine
	@echo "waiting for postgres to accept connections..."
	@until docker exec glutenfree-pg-test psql -U root -d glutenfree_test -c 'select 1' >/dev/null 2>&1; do sleep 0.5; done
	@migrate -path db/migration -database "$(TEST_DB_URL)" up && \
		TEST_DB_SOURCE="$(TEST_DB_URL)" go test -tags=integration -count=1 -v ./db/... ; \
		status=$$? ; \
		docker rm -f glutenfree-pg-test >/dev/null 2>&1 || true ; \
		exit $$status

.PHONY: postgres createdb dropdb migrateup migrateup1 migratedown migratedown1 new_migration sqlc server build test test-integration
