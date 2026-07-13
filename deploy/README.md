# Pantawin — VPS deploy runbook (M0)

Target: `103.181.182.61`, root SSH. This stack is entirely additive — it must
never touch `anana`, MySQL, PM2 apps, or the existing nginx sites. Every step
below re-checks those are still healthy before proceeding to the next.

## 0. Pre-flight baseline (re-run each time, compare after every step)

```sh
free -h
docker ps
docker stats --no-stream
ss -tlnp
pm2 list
```

## 1. First-time setup

```sh
mkdir -p /opt/pantawin
git clone https://github.com/tgz99/pantawin.git /opt/pantawin
cd /opt/pantawin/deploy
cp .env.example .env
# Fill in POSTGRES_PASSWORD (openssl rand -base64 32), JWT_SECRET (same),
# ADMIN_EMAIL, ADMIN_PASSWORD. Then:
chmod 600 .env
```

## 2. Bring the stack up, one service at a time

```sh
cd /opt/pantawin/deploy
docker compose -f docker-compose.prod.yml --env-file .env up -d postgres
docker compose -f docker-compose.prod.yml exec postgres pg_isready -U pantawin
free -h && docker ps   # confirm anana-db-1 / anana-anana-landing-1 still Up

docker compose -f docker-compose.prod.yml --env-file .env up -d redis
docker compose -f docker-compose.prod.yml exec redis redis-cli ping
free -h

# Building the Go image happens here (docker compose build). This is a
# small module with few dependencies — expect a brief, transient memory
# bump during compilation, not a sustained increase. Watch `free -h` /
# `docker stats` during this step; if it looks alarming, stop
# (docker compose down) before it affects anything else on the box.
docker compose -f docker-compose.prod.yml --env-file .env up -d --build api
docker compose -f docker-compose.prod.yml logs api
curl -sf http://127.0.0.1:8081/healthz
curl -sf http://127.0.0.1:8081/v1/monitors  # 401 is expected here (no auth header) — confirms routing works
free -h && docker ps && pm2 list   # full health re-check
```

## 3. nginx vhost (HTTP only, before any DNS/TLS)

```sh
cp /opt/pantawin/deploy/nginx/api.pantawin.gratisaja.com.conf \
   /etc/nginx/sites-available/api.pantawin.gratisaja.com
ln -s /etc/nginx/sites-available/api.pantawin.gratisaja.com \
      /etc/nginx/sites-enabled/api.pantawin.gratisaja.com
nginx -t                      # syntax check BEFORE reloading — zero risk if it fails here
systemctl reload nginx        # reload, never restart
curl -sI https://qr.gratisaja.com   # confirm an existing site is unaffected
```

## 4. DNS + TLS (once Cloudflare DNS for api.pantawin.gratisaja.com is live)

```sh
# From the dev machine, confirm it resolves first:
#   nslookup api.pantawin.gratisaja.com
certbot --nginx -d api.pantawin.gratisaja.com --non-interactive --agree-tos \
  -m <your-email> --redirect
nginx -t && systemctl reload nginx
curl -s https://api.pantawin.gratisaja.com/healthz
```

## 5. Ongoing memory watch (first 48-72h after each deploy)

Trigger for rollback: `available` (from `free -h`) sustained under ~150MB for
more than ~10 minutes, or `vmstat 1 5` showing repeated nonzero `si`/`so`
(active swapping, not just resident swap usage).

## Rollback (new stack only — never touches anana/MySQL/PM2/nginx configs for other sites)

```sh
cd /opt/pantawin/deploy
docker compose -f docker-compose.prod.yml down   # NOT `down -v` — that would delete the pg volume
# If the nginx vhost itself needs pulling too:
rm /etc/nginx/sites-enabled/api.pantawin.gratisaja.com
nginx -t && systemctl reload nginx
```

## Redeploying after code changes

```sh
cd /opt/pantawin
git pull
cd deploy
docker compose -f docker-compose.prod.yml --env-file .env up -d --build api
```

## M2 — Email alerts (self-hosted Postfix, separate step)

Run this only after M0/M1 have been stable for a while. Postfix is host-installed
(systemd), NOT containerized — see the rationale in the script header. It never
touches nginx/docker/anana; kill-switch is `systemctl stop postfix opendkim`.

```sh
cd /opt/pantawin/deploy/postfix
./install-postfix.sh        # idempotent; installs postfix+opendkim, prints DNS records
```

The script prints the SPF, DKIM, and DMARC TXT records to add in Cloudflare
(gratisaja.com zone). After DNS propagates, smoke-test before trusting it:

```sh
apt-get install -y swaks
swaks --to you@gmail.com --from alerts@pantawin.gratisaja.com --server 127.0.0.1
```

The API already reaches Postfix via the Docker host gateway (SMTP_ADDR in
`.env`). Until Postfix is installed, alert sends fail gracefully and are logged
in the `notification_log` table with `ok=false` — the app runs fine without it.

**Known limitation:** the VPS PTR is `103-181-182-61.domainesia.io` (not under
gratisaja.com, not changeable on a shared VPS) and the IP has no sending
reputation, so early mail may land in spam at Gmail/Outlook even with correct
SPF/DKIM. This is inherent to self-hosting on this box.
