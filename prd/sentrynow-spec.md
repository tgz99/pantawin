# Product & Technical Specification — "SentryNow" — Uptime Monitor
**Version:** 1.0 · **Date:** July 2026 · **Status:** Draft for build
**Platforms:** Android first (Android Studio), iOS later via shared KMP core
**Methodology:** TDD-enforced (no production code without a failing test first)

---

## 1. Product Overview

**Brand:** SentryNow · **Android package:** `com.sentrynow.app` (KMP shared: `com.sentrynow.shared`) · **Deep link scheme:** `sentrynow://` · **Notification channels branded "SentryNow Alerts".** Verify name availability on Play Store and trademark before publishing.

A self-hosted uptime monitoring system (UptimeRobot-style) consisting of:

1. **Server (VPS)** — performs continuous URL checks, stores results, computes analytics, pushes real-time alerts.
2. **Mobile App (Android → iOS)** — manage monitors, receive real-time alerts, view analytics (daily / weekly / monthly / annual).

### Core user stories
| ID | Story | Priority |
|----|-------|----------|
| US-1 | As a user, I add a URL with a check interval so the server monitors it 24/7 | P0 |
| US-2 | As a user, I receive a push alert within seconds when a monitor goes DOWN or recovers (UP) | P0 |
| US-3 | As a user, I see uptime %, response-time trends, and incident history per monitor | P0 |
| US-4 | As a user, I switch analytics between daily, weekly, monthly, annual views | P0 |
| US-5 | As a user, I pause/resume/edit/delete monitors | P1 |
| US-6 | As a user, I configure alert rules (e.g., alert only after N consecutive failures) | P1 |
| US-7 | As a user, I view a status dashboard with all monitors at a glance | P0 |
| US-8 | As a user, I also receive email alerts (per-monitor toggle: push, email, or both) | P1 |

### Out of scope (v1)
Multi-user teams, public status pages, SSL expiry monitoring, keyword monitoring, SMS/WhatsApp channels. These are v2 candidates — architecture must not block them.

---

## 2. Architecture Decision: Portability Strategy

**Decision: Kotlin Multiplatform (KMP) shared core + native UI.**

- **Shared module (`shared/`):** domain models, use cases, repositories, API client (Ktor), local cache (SQLDelight), analytics aggregation logic. ~70% of non-UI code reused on iOS.
- **Android UI:** Jetpack Compose.
- **iOS UI (later):** SwiftUI consuming the same shared module.

Rationale: Flutter/React Native would give one codebase but you've standardized on Kotlin + Compose (same stack as The Kalcer), and KMP keeps business logic test coverage portable — TDD tests written once in `commonTest` run on both platforms. Native UI also gives best push-notification and background behavior fidelity per platform.

---

## 3. System Architecture

```
┌─────────────┐   HTTPS/REST + WebSocket   ┌──────────────────────────┐
│  Mobile App │◄──────────────────────────►│  API Server (VPS)        │
│  (KMP core) │◄── FCM push (alerts) ──────│  Kotlin/Ktor  (or Go)    │
└─────────────┘                            │  ┌────────────────────┐  │
                                           │  │ Scheduler / Worker │──┼──► HTTP checks to
                                           │  │ (check engine)     │  │    monitored URLs
                                           │  └────────────────────┘  │
                                           │  PostgreSQL + Redis      │
                                           └──────────────────────────┘
```

### 3.1 Server stack (VPS)
| Component | Choice | Notes |
|-----------|--------|-------|
| Language/framework | **Kotlin + Ktor** | One language across server + app + shared module; JUnit/Kotest TDD everywhere. (Alternative: Go if you want lower RAM footprint.) |
| Database | **PostgreSQL 16** | Monitors, users, incidents, check results |
| Time-series storage | Postgres partitioned table (`check_results` partitioned by month) | Avoids extra infra; TimescaleDB optional later |
| Queue/cache | **Redis** | Check scheduling queue, rate limiting, WebSocket pub/sub |
| Push | **Firebase Cloud Messaging** | Works for Android now; FCM also delivers to iOS (APNs) later |
| Realtime in-app | **WebSocket** (Ktor) | Live status updates while app is foregrounded |
| Deploy | Docker Compose on VPS, Nginx reverse proxy, Let's Encrypt | Single VPS is sufficient for hundreds of monitors |

### 3.2 Check engine design
- Each monitor has `interval_seconds` (min 30s, default 60s).
- Scheduler loop pops due checks from a Redis sorted set (score = next-run epoch); workers execute HTTP checks with configurable timeout (default 10s), method (GET/HEAD), expected status codes (default 200–399).
- **Failure confirmation:** a DOWN state is declared only after `failure_threshold` consecutive failures (default 2) re-checked from a short retry (30s apart) — prevents false alerts.
- Each check records: `status`, `http_code`, `response_time_ms`, `error_type` (timeout/dns/tls/conn/http), `checked_at`.
- State transitions (UP→DOWN, DOWN→UP) create an **Incident** row and hand off to the **Notification Dispatcher**.

### 3.2b Notification Dispatcher (multi-channel)
A single abstraction so channels are pluggable (v2: WhatsApp, Telegram, SMS slot in without touching the engine):

```kotlin
interface AlertChannel { suspend fun send(incident: IncidentEvent, target: ChannelTarget): Result<Unit> }
// v1 implementations: FcmChannel, EmailChannel, WebSocketChannel
```

- **Email:** SMTP via transactional provider — **Resend** or **Brevo** (both have free tiers ~100–300 emails/day, fine for alerts) — or plain SMTP relay if you prefer zero vendors. HTML template: monitor name, status, reason, duration (on recovery), deep link.
- Per-monitor `alert_channels` config: `["push"]`, `["email"]`, or `["push","email"]`.
- Delivery is queued (Redis) with retry + exponential backoff; the check engine never blocks on notification I/O.
- Idempotency key = `incident_id + channel + event_type` — guarantees exactly one email and one push per transition even across worker retries.

### 3.3 Analytics aggregation
- Raw results kept 90 days.
- Hourly rollup job writes to `stats_hourly` (uptime %, avg/p95 response time, check count, fail count).
- Daily rollup to `stats_daily` (kept forever).
- API serves: **daily** view from hourly rows, **weekly/monthly** from daily rows, **annual** from monthly aggregation of daily rows.
- Uptime % = `1 − (down_seconds / monitored_seconds)`, computed from incident durations, not naive check counts (more accurate).

---

## 4. API Specification (REST, JSON, JWT auth)

Base: `https://api.yourdomain.com/v1`

| Method | Endpoint | Purpose |
|--------|----------|---------|
| POST | `/auth/register`, `/auth/login`, `/auth/refresh` | Auth (JWT access 15m + refresh 30d) |
| POST | `/devices` | Register FCM token |
| GET | `/monitors` | List monitors + current status |
| POST | `/monitors` | Create `{url, name, interval_seconds, method, timeout_ms, failure_threshold}` |
| GET/PATCH/DELETE | `/monitors/{id}` | Detail / edit / delete |
| POST | `/monitors/{id}/pause` · `/resume` | Pause/resume |
| GET | `/monitors/{id}/incidents?from&to` | Incident history |
| GET | `/monitors/{id}/stats?period=daily\|weekly\|monthly\|annual&date=` | Aggregated analytics |
| GET | `/monitors/{id}/checks?limit=` | Recent raw checks (response-time sparkline) |
| WS | `/ws` | Real-time status/incident events |

**Stats response shape (example, `period=weekly`):**
```json
{
  "monitor_id": "m_123",
  "period": "weekly",
  "range": {"from": "2026-07-06", "to": "2026-07-12"},
  "uptime_pct": 99.82,
  "avg_response_ms": 231,
  "p95_response_ms": 512,
  "incidents": 1,
  "downtime_seconds": 1088,
  "buckets": [
    {"ts": "2026-07-06", "uptime_pct": 100, "avg_ms": 220},
    {"ts": "2026-07-07", "uptime_pct": 98.7, "avg_ms": 260}
  ]
}
```

**FCM alert payload:**
```json
{"type": "incident", "monitor_id": "m_123", "monitor_name": "unitagstore.com",
 "status": "DOWN", "reason": "timeout", "at": "2026-07-12T03:14:00Z"}
```

---

## 5. Database Schema (server, PostgreSQL)

```sql
users(id, email, password_hash, created_at)
devices(id, user_id, fcm_token, platform, created_at)
monitors(id, user_id, name, url, method, interval_seconds, timeout_ms,
         expected_status_min, expected_status_max, failure_threshold,
         status ENUM('UP','DOWN','PAUSED','PENDING'),
         alert_channels TEXT[] DEFAULT '{push}', alert_email TEXT NULL, created_at)
notification_log(id, incident_id, channel, event_type, sent_at, ok BOOL)  -- idempotency + audit
check_results(id, monitor_id, checked_at, ok BOOL, http_code, response_time_ms,
              error_type) PARTITION BY RANGE (checked_at)
incidents(id, monitor_id, started_at, resolved_at NULL, cause, notified BOOL)
stats_hourly(monitor_id, hour_ts, checks, fails, up_pct, avg_ms, p95_ms)
stats_daily(monitor_id, day, checks, fails, up_pct, avg_ms, p95_ms, downtime_s)
```

---

## 6. Mobile App Specification (Android)

### 6.1 Stack
- Kotlin, **Jetpack Compose**, Material 3
- **MVVM + Clean Architecture** (presentation → domain → data), consistent with your The Kalcer conventions
- **Hilt** DI (Android layer), **Koin or manual DI** in KMP shared module
- **Ktor client** (shared), **SQLDelight** (shared, offline cache), **DataStore** (settings/tokens)
- **Vico or Compose Charts** for analytics graphs
- FCM for push; WebSocket for foreground live updates
- **Min SDK 30 (Android 11)**, target SDK latest (36). Rationale: guarantees modern TLS, scoped storage, cleaner background/notification behavior, and drops all pre-30 compat shims; API 30+ covers the vast majority of active devices in 2026. Dynamic color still gracefully falls back to the branded palette on Android 11.

### 6.2 Module layout
```
project/
├── shared/            # KMP: models, usecases, repos, api, db, analytics logic
│   ├── commonMain/
│   ├── commonTest/    # bulk of TDD tests live here — portable to iOS
│   ├── androidMain/
│   └── iosMain/       # stubs now, filled during iOS port
├── app/               # Android: Compose UI, ViewModels, Hilt, FCM service
└── server/            # Ktor backend (same repo or separate — your call)
```

### 6.3 Screens
1. **Dashboard** — monitor cards (name, URL, status dot, uptime 24h, last response time, mini sparkline); pull-to-refresh; live status via WebSocket; FAB to add monitor.
2. **Add/Edit Monitor** — URL (validated), name, interval picker, advanced (method, timeout, failure threshold). Test-check button before save.
3. **Monitor Detail** — current status + duration, period selector chips **(Day / Week / Month / Year)**, uptime % ring, response-time line chart, uptime bar chart per bucket, incident list.
4. **Incident Detail** — timeline, cause, duration.
5. **Alerts/Settings** — notification preferences, quiet hours, account, logout.

### 6.4 Real-time behavior
- **Foreground:** WebSocket updates dashboard status instantly.
- **Background/killed:** FCM high-priority data message → local notification (channel: "Downtime Alerts", importance HIGH, distinct sound). Tapping opens Monitor Detail via deep link `sentrynow://monitor/{id}`.
- Notification for DOWN and RECOVERED (with downtime duration).

### 6.5 Offline behavior
Monitors list and last-known stats cached in SQLDelight; app opens instantly from cache, then syncs. Mutations (add/edit) require connectivity (v1); show clear error state.

### 6.6 Design System & Iconography (production-grade)

**Icon library: Material Symbols (Rounded), single weight, via `material-icons-extended` + custom vector assets for anything missing.** One family across the whole app — no mixed icon sets. All icons are vector drawables (no PNGs), tinted via theme tokens so they adapt to light/dark automatically.

**App icon (launcher):**
- Adaptive icon (foreground + background layers) plus monochrome layer for Android 13+ themed icons.
- Concept: sentry/shield mark with a pulse line through it (watchful guardian motif) — must stay legible at 48dp.
- Full asset set via Android Studio Image Asset tool; Play Store 512×512 hi-res + feature graphic.

**Semantic status iconography (never color alone — accessibility):**
| State | Icon | Color token |
|-------|------|-------------|
| UP | `check_circle` (filled) | `statusUp` green |
| DOWN | `error` (filled) | `statusDown` red |
| PAUSED | `pause_circle` | `statusPaused` amber/neutral |
| PENDING (first check) | `pending` / pulsing dot | grey |
| DEGRADED (up but slow) | `warning` | amber |

Status is always icon + color + text label ("Up"/"Down") — colorblind-safe and WCAG-contrast-checked in both themes.

**Functional icon map (consistent everywhere):**
- Add monitor: `add` (FAB) · Edit: `edit` · Delete: `delete` · Pause/Resume: `pause`/`play_arrow`
- Refresh: `sync` · Analytics: `monitoring` · Incident history: `history`
- Notification settings: `notifications` · Email channel: `mail` · Push channel: `notifications_active`
- Response time: `speed` · Uptime: `trending_up` · Interval: `schedule` · URL: `link`
- Settings: `settings` · Account: `person` · Logout: `logout` · Quiet hours: `bedtime`

**Notification icons (Android requirement — commonly botched in production):**
- Status-bar small icon MUST be a pure white-on-transparent vector (Android renders it monochrome; a colored icon shows as a blank square). Provide `ic_stat_alert` — 24dp white silhouette of the SentryNow shield mark.
- Accent via `setColor()`: red for DOWN, green for RECOVERED.
- Large icon: cached monitor favicon if available, else app mark.
- Two channels: "Downtime Alerts" (IMPORTANCE_HIGH, distinct sound + vibration) and "Recovery" (IMPORTANCE_DEFAULT).

**Theming & polish checklist (the production-ready bar):**
- Material 3 dynamic color (Android 12+) with branded fallback palette; full light + dark themes.
- Design tokens file (colors, 4dp spacing grid, type scale) in `ui/theme/` — no hardcoded hex inside composables.
- Every screen has explicit **loading / empty / error / offline** states (empty dashboard = illustration + "Add your first monitor" CTA, never a blank list).
- Skeleton shimmer on dashboard load; standard pull-to-refresh indicator.
- Touch targets ≥ 48dp; `contentDescription` on every icon; TalkBack pass required before beta.
- Splash screen via `androidx.core.splashscreen`; predictive back gesture support.
- Charts: theme-driven series colors, labeled axes, tap-to-inspect tooltips.

**TDD applies to UI states too:** Paparazzi screenshot tests for each screen in light/dark × loading/empty/error/populated — visual-state regressions fail CI, not QA.

---

## 7. TDD Mandate & Test Strategy

**Rule: red → green → refactor. No production code is merged without a failing test written first. PRs must show test commits preceding implementation commits.**

### 7.1 Test pyramid
| Layer | Tooling | Coverage target |
|-------|---------|-----------------|
| Shared domain (use cases, uptime math, bucket aggregation, state machine) | Kotest/JUnit in `commonTest` | **90%+** — this is the crown jewel |
| Data layer (repos, API client) | Ktor MockEngine, SQLDelight in-memory driver | 85% |
| ViewModels | JUnit + Turbine (Flow testing) + fake repos | 85% |
| Compose UI | Compose UI tests (critical flows: add monitor, view stats, receive alert deep link) | Key journeys |
| Server | Kotest + Testcontainers (Postgres, Redis); WireMock for target-URL simulation | 85% |
| E2E | Maestro (mobile) + docker-compose test env | Smoke suite |

### 7.2 Critical test-first specifications (write these tests before any code)
1. **Monitor state machine:** UP→DOWN only after N consecutive fails; DOWN→UP on first success; PAUSED ignores checks. Property-based tests on random check sequences.
2. **Uptime calculation:** given incident windows, uptime % is exact; boundary cases — incident spanning midnight, spanning month boundary, unresolved (ongoing) incident, monitor paused mid-period.
3. **Bucket aggregation:** daily→weekly→monthly→annual rollups are consistent (sum of parts == whole); timezone handling (user in WIB/UTC+7, server stores UTC — buckets must render in user TZ).
4. **URL validation:** scheme required, IDN domains, rejects local/private IPs (SSRF guard on server).
5. **Alert idempotency (per channel):** one incident = exactly one DOWN and one RECOVERED notification *per enabled channel* (email and push), even with worker retries — verified against `notification_log`.
6. **Scheduler fairness:** a slow/timing-out monitor never delays other monitors' checks.
7. **Auth:** expired access token → silent refresh → retry; refresh failure → logout flow.

### 7.3 CI gates (GitHub Actions)
`test → coverage check (fail < target) → lint (detekt/ktlint) → build`. Server: same + Testcontainers integration suite. Merges blocked on red.

---

## 8. Security & Ops
- HTTPS only, JWT with rotation, bcrypt/argon2 password hashing.
- **SSRF protection:** server resolves target hostnames and rejects private/loopback ranges before checking.
- Rate limiting (Redis) on auth and monitor-creation endpoints.
- Per-user monitor quota (e.g., 50) to protect the VPS.
- Backups: nightly `pg_dump` to object storage; Docker Compose + `.env` secrets; health endpoint `/healthz` (monitor your monitor with an external free check — eat your own dog food carefully).
- Observability: structured logs (JSON), Prometheus metrics (check latency, queue depth) — optional Grafana.

---

## 9. Incremental Roadmap — no big bang

Principle: **every milestone ends with something you actually use in production**, on your real VPS, monitoring real URLs. Each slice is vertical (server + app together where relevant), TDD-gated, and independently valuable. If the project stops at any milestone, you still have a working tool.

### M0 — Walking Skeleton *(≈1 week)*
Thin end-to-end slice: repo scaffold (KMP shared + app + server), CI with coverage gates, Docker Compose on VPS, one hardcoded URL checked every 60s, result written to Postgres, Android app with a single screen showing UP/DOWN fetched from the API.
**Ship & use:** it monitors one real URL (e.g., unitagstore.com). Ugly but alive.
**Exit:** deploy pipeline works; e2e smoke test green in CI; state visible on your phone.

### M1 — Usable Monitor Manager *(≈1–2 weeks)*
Monitor CRUD (API + app screens), interval config, URL validation + SSRF guard, real state machine with failure-threshold confirmation, single-user API-key auth (defer full auth), SQLDelight offline cache. **Design system lands here:** theme tokens, icon set, adaptive launcher icon, loading/empty/error states, Paparazzi screenshot tests in CI.
**Ship & use:** you manage 5–10 real monitors from your phone daily.
**Exit:** state-machine property tests + SSRF suite green; Maestro smoke: add → see status → delete.

### M2 — Email Alerts *(≈1 week)*
Incidents table, Notification Dispatcher, **EmailChannel first** (simpler than FCM — no app-side work), idempotency via `notification_log`, HTML templates for DOWN/RECOVERED.
**Ship & use:** real downtime alerts land in your inbox. The product is already genuinely useful with zero push infrastructure.
**Exit:** alert idempotency suite green; kill a test container, receive exactly one DOWN + one RECOVERED email.

### M3 — Push Alerts & Realtime *(≈1–2 weeks)*
FCM integration (server + app), notification channel + deep links, WebSocket live dashboard, per-monitor channel toggle (push/email/both). Includes **POST_NOTIFICATIONS runtime permission flow** (Android 13+): pre-permission rationale screen, graceful degraded state if denied (banner: "Push alerts are off — email alerts still active"), settings deep link to re-enable.
**Ship & use:** sub-10-second downtime notifications on your phone.
**Exit:** alert E2E on physical device; dispatcher tests prove push+email both fire once each.

### M4 — Analytics v1: Daily & Weekly *(≈1–2 weeks)*
Hourly + daily rollup jobs, uptime-from-incident-duration math, stats API, Monitor Detail screen with uptime ring + response-time chart, Day/Week period chips only.
**Ship & use:** answer "how was this site this week?" from your phone.
**Exit:** uptime-math boundary suite green (midnight span, ongoing incident, paused windows); rollup consistency tests green.

### M5 — Analytics v2: Monthly & Annual + Incident History *(≈1 week)*
Monthly/annual aggregation, incident list + detail screens, timezone-correct bucketing (WIB rendering over UTC storage).
**Exit:** sum-of-parts == whole consistency suite green across all four periods; TZ tests green.

### M6 — Hardening & Beta *(≈1 week)*
Full auth (register/login/JWT refresh) if going multi-user, rate limits, quotas, nightly backups, Prometheus metrics, Play internal testing track.
**Exit:** load test — 200 monitors @60s on 2GB VPS < 50% CPU; auth refresh suite green.

### M7 — iOS Port *(later, ≈2–3 weeks)*
SwiftUI screens over the existing shared module; APNs via FCM.
**Exit:** `commonTest` suite green on iOS target; feature parity checklist.

> **Sequencing logic:** email before push (M2 before M3) because email needs no mobile plumbing — you get real alerting value one full milestone earlier. Analytics split in two so charts ship on real accumulated data (by M4 you'll have weeks of check history to render).

---

## 10. Open decisions for you
1. Server language: **Kotlin/Ktor** (recommended, one-language stack) vs Go (lighter VPS footprint)?
2. Monorepo (shared + app + server together) vs separate server repo?
3. Minimum check interval — 30s or 60s (affects VPS load and quota design)?
4. v1 auth: single-user (just you) with a simple API key, or full multi-user register/login from day one? Single-user cuts M1 scope by ~30%.
