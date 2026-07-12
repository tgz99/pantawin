FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go vet ./...
# Unit tests only (no -tags=integration) — the integration suite needs a
# Docker daemon inside the build, which isn't available in a build stage.
RUN CGO_ENABLED=0 go test ./...
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/pantawin-api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=builder /out/pantawin-api ./pantawin-api
COPY migrations ./migrations

EXPOSE 8080
ENTRYPOINT ["./pantawin-api"]
