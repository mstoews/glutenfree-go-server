# glutenfree-server

Go REST API backend for **GlutenFree** — a subscription gluten-free restaurant
finder for Japan (Tokyo-first). See the iOS app + full design doc in the
companion repo (`GlutenFree/docs/design.md`).

## Stack

- **gin** — HTTP router
- **sqlc** (pgx/v5) — type-safe SQL → Go in `db/sqlc`
- **golang-migrate** — versioned schema migrations in `db/migration`
- **viper** — config from `app.env`
- **golang-jwt/jwt v5** — access/refresh tokens (HS256)
- **bcrypt** — password hashing

## Layout

```
cmd/        main entry
app/        config loading (Application)
runtime/    pgx pool + server bootstrap
api/        gin server, middleware, handlers
token/      JWT maker + payload
util/       config + password helpers
db/
  migration/  golang-migrate SQL (NNNNNN_*.up/down.sql)
  query/      sqlc source queries
  sqlc/       generated code (do not edit *.sql.go / models.go / db.go)
```

## Local development

```bash
cp app.env.example app.env      # then edit secrets
make postgres                   # start docker postgres
make createdb
make migrateup                  # apply migrations
make sqlc                       # regenerate db/sqlc after query changes
make server                     # run on :8080
```

## Implemented (slice 1 — auth foundation)

| Method | Path                  | Auth | Notes                                  |
|--------|-----------------------|------|----------------------------------------|
| GET    | /health               | no   | liveness                               |
| POST   | /auth/register        | no   | email + password (bcrypt)              |
| POST   | /auth/login           | no   | returns access + refresh tokens        |
| POST   | /auth/refresh         | no   | exchange refresh token for new access  |
| GET    | /subscription/status  | yes  | current tier + expiry                  |

Not yet built: Sign in with Apple (`/auth/apple`), StoreKit verify
(`/subscription/verify`) + Apple webhook, stores/menu endpoints, admin + internal
ops routes. See design doc + brainz `glutenfree:open-threads`.
