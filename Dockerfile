# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.22-alpine AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0 → a fully static binary for the distroless runtime.
# -s -w strips DWARF/debug to shrink the image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
# Bundle migrations so they can be applied from the image (e.g. an init job).
COPY db/migration /app/db/migration

EXPOSE 8080
USER nonroot:nonroot
# Config comes from environment variables (no app.env in the image). At minimum
# set DB_SOURCE, TOKEN_SYMMETRIC_KEY, HTTP_SERVER_ADDRESS=0.0.0.0:8080.
ENTRYPOINT ["/app/server"]
