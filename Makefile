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

.PHONY: postgres createdb dropdb migrateup migrateup1 migratedown migratedown1 new_migration sqlc server build test
