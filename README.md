# Pantawin

Self-hosted uptime monitor — Go API + check engine on a VPS, Android app
(Kotlin Multiplatform shared core + Jetpack Compose UI). Spec:
[prd/sentrynow-spec.md](prd/sentrynow-spec.md) (the product was renamed from
the spec's working title "SentryNow" to **Pantawin**; the spec is kept
verbatim as the historical v1.0 document).

## Layout

| Path | What |
|---|---|
| `server/` | Go API + scheduler (chi, pgx, go-redis, goose migrations) |
| `shared/` | KMP module: API DTOs + Ktor client (SQLDelight cache from M1) |
| `app/` | Android app (Compose, Material 3) |
| `deploy/` | VPS artifacts: docker-compose, nginx vhost, tuning configs, runbook |

## Server — local dev

```sh
cd server
go vet ./...
go test ./...                    # unit tests, no Docker needed
go test -tags=integration ./...  # integration tests — needs a Docker daemon
```

Run locally: set `DATABASE_URL`, `REDIS_ADDR`, `JWT_SECRET`, `ADMIN_EMAIL`,
`ADMIN_PASSWORD` (see `deploy/.env.example`), then `go run ./cmd/api` from
`server/` (migrations run automatically on boot).

## Android — local dev

Requires JDK 21 (Android Studio's bundled JBR works) and the Android SDK.

```sh
./gradlew :shared:jvmTest :app:testDebugUnitTest   # tests
./gradlew :app:assembleDebug                        # APK
```

Copy `app/secrets.properties.example` → `app/secrets.properties` and fill in
the bootstrap admin credentials (M0 only — a real login screen lands in M1).

## Deploy

See [deploy/README.md](deploy/README.md) — the full VPS runbook, including
health checks around every step and the rollback procedure.

## Roadmap

Incremental milestones per spec section 9: **M0** walking skeleton (current),
M1 monitor CRUD + design system, M2 email alerts, M3 push/realtime,
M4-M5 analytics, M6 hardening, M7 iOS.
