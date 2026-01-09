# Uni API Web

[English](./README.md) | [Chinese](./README_CN.md)

Uni API Web is a **full LLM API console + gateway backend**: accounts, API keys, model catalog & pricing, request logs, admin configuration (channels/models/users), and announcements.

This repo contains:
- **Console (Next.js 16 + TypeScript + Tailwind v4 + shadcn/ui)**: repo root
- **Backend (FastAPI + Postgres)**: `apps/api`

## Features

**User**
- Landing page (dark, glass, micro-interactions)
- Auth: email/password + email verification codes (Resend) + Google OAuth (PKCE)
- Dashboard: usage/spend trend, remaining balance, announcements (server data)
- API Keys: create/revoke/restore/delete; masked by default; **copy full key anytime**; last used + total spend
- Models: list available models + input/output prices (priced in $/M tokens; UI shows as `$X`)
- Logs: model/time/tokens/latency/TTFT/TPS/cost/source IP for every request
- Profile: account info + delete account

**Admin**
- Channels: configure upstream `baseUrl` + `apiKey`; restrict which user groups may use each channel
- Model Config: aggregate `/v1/models` from channels (dedupe), enable/disable models and set pricing; manual refresh
- Users: ban/unban, delete, update balance, update role/group (owner-protected)
- Announcements: publish/edit/delete; shown on dashboards with timestamps

**UX / Performance**
- Console & admin routes use “skeleton first, then stream in” (`loading.tsx` + `Suspense`)
- Lightweight route transitions (CSS-only, respects `prefers-reduced-motion`)
- i18n: `zh-CN` + `en`, auto-detected and persisted in `uai_locale` cookie

## Repo Layout

```text
.
├─ src/                 # Next.js Console
├─ src/proxy.ts         # Next.js Proxy: auth gate + /v1/* reverse proxy to backend
├─ apps/api/            # FastAPI Backend
├─ docker-compose.yml   # Local/dev/prod (db + api + optional web)
└─ .github/workflows/   # CI: build and push Docker images
```

## Local Development (recommended)

1) Create env file:
```bash
cp .env.example .env
```
Minimum required:
- `POSTGRES_PASSWORD` (docker compose enforces this)
- For login: `GOOGLE_*` and/or `RESEND_*`

2) Start backend (Postgres + API):
```bash
docker compose up -d --build db api
```
Backend: `http://localhost:8001` (host port), health: `GET http://localhost:8001/v1/health`

3) Start console (Next.js dev):
```bash
npm install
npm run dev
```
Console: `http://localhost:3000`

One-liner you’ll use often:
```bash
docker compose up -d --build db api && npm install && npm run dev
```

## Run Everything in Docker

```bash
docker compose up -d --build
```

- Console container: `http://localhost:3000`
- Backend host port: `http://localhost:8001` → backend container listens on `8000`
- `/v1/*` is rewritten by Next.js Proxy to `API_BASE_URL`

## Admin (Owner/Admin) Bootstrap

- The **first user registered on an empty database** becomes **Owner**.
- To grant admin later:
  1) Set a strong `ADMIN_BOOTSTRAP_TOKEN` on the backend
  2) The user logs in and calls:
     ```bash
     curl -X POST 'http://localhost:8001/v1/auth/admin/claim' \
       -H 'Content-Type: application/json' \
       -H "Authorization: Bearer <SESSION_TOKEN>" \
       -d '{"token":"<ADMIN_BOOTSTRAP_TOKEN>"}'
     ```

## Google OAuth (Web Client) Setup

1) Google Cloud Console → APIs & Services → Credentials
2) Create credentials → OAuth client ID → **Web application**
3) Add **Authorized redirect URIs** (must match `GOOGLE_REDIRECT_URI` exactly):
   - Local: `http://localhost:3000/api/auth/google/callback`
   - Production: `https://<your-domain>/api/auth/google/callback`
4) Fill env vars:
   - Console (Next): `GOOGLE_CLIENT_ID`, `GOOGLE_REDIRECT_URI`
   - Backend (FastAPI): `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI`

## Email Verification (Resend)

- Set `RESEND_API_KEY` and `RESEND_FROM_EMAIL`
- `EMAIL_VERIFICATION_REQUIRED=true` enforces verification on auth flows
- `EMAIL_VERIFICATION_TTL_MINUTES` controls code TTL (default `10`)

## Deployment (Docker Images + Docker Hub)

GitHub Actions builds and pushes two images:
- `DOCKERHUB_USERNAME/uni-api-frontend`
- `DOCKERHUB_USERNAME/uni-api-backend`

Workflow: `.github/workflows/docker-build-push.yml`
Required GitHub Secrets:
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN` (Docker Hub access token with push scope)

**A) Build from source on the server**
```bash
docker compose up -d --build
```

**B) Pull prebuilt images from Docker Hub (recommended)**

Create a production compose file (example: `docker-compose.prod.yml`):
```yaml
services:
  web:
    image: DOCKERHUB_USERNAME/uni-api-frontend:main
    restart: unless-stopped
    depends_on: [api]
    environment:
      API_BASE_URL: http://api:8000/v1
      APP_NAME: ${APP_NAME:-MyApp}
      GOOGLE_CLIENT_ID: ${GOOGLE_CLIENT_ID:-}
      GOOGLE_REDIRECT_URI: ${GOOGLE_REDIRECT_URI:-}
      NEXT_TELEMETRY_DISABLED: 1
      NODE_ENV: production
    ports: ["3000:3000"]

  db:
    image: postgres:17.6-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: ${POSTGRES_USER:-uniapi}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?Set POSTGRES_PASSWORD in .env}
      POSTGRES_DB: ${POSTGRES_DB:-uniapi}
    volumes:
      - uniapi_pg_data:/var/lib/postgresql/data

  api:
    image: DOCKERHUB_USERNAME/uni-api-backend:main
    restart: unless-stopped
    depends_on: [db]
    environment:
      DATABASE_URL: postgresql+asyncpg://${POSTGRES_USER:-uniapi}:${POSTGRES_PASSWORD:?Set POSTGRES_PASSWORD in .env}@db:5432/${POSTGRES_DB:-uniapi}
      APP_ENV: prod
      APP_NAME: Uni API Backend
      API_PREFIX: /v1
      SESSION_TTL_DAYS: 7
      GOOGLE_CLIENT_ID: ${GOOGLE_CLIENT_ID:-}
      GOOGLE_CLIENT_SECRET: ${GOOGLE_CLIENT_SECRET:-}
      GOOGLE_REDIRECT_URI: ${GOOGLE_REDIRECT_URI:-}
      ADMIN_BOOTSTRAP_TOKEN: ${ADMIN_BOOTSTRAP_TOKEN:-}
      RESEND_API_KEY: ${RESEND_API_KEY:-}
      RESEND_FROM_EMAIL: ${RESEND_FROM_EMAIL:-Uni API <onboarding@resend.dev>}
      EMAIL_VERIFICATION_REQUIRED: ${EMAIL_VERIFICATION_REQUIRED:-true}
      EMAIL_VERIFICATION_TTL_MINUTES: ${EMAIL_VERIFICATION_TTL_MINUTES:-10}
    ports: ["8001:8000"]

volumes:
  uniapi_pg_data:
```

Update flow:
```bash
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
```

Security notes:
- Do not bake secrets into images; inject via runtime env vars.
- Do not put secrets in `NEXT_PUBLIC_*` (they are exposed to browsers).

## Environment Variables (complete reference)

### Root `.env` (shared by Next + docker compose)

**Required (docker compose)**
- `POSTGRES_PASSWORD`: Postgres password (must be strong)

**Console / Proxy (Next.js)**
- `API_BASE_URL`: backend base URL (default `http://localhost:8001/v1`), used by:
  - server-side backend calls (`src/lib/backend.ts`)
  - `/v1/*` proxy rewrite (`src/proxy.ts`)
- `APP_NAME`: site/app name (default `MyApp`)
- `GOOGLE_CLIENT_ID`: Google OAuth client ID
- `GOOGLE_REDIRECT_URI`: OAuth redirect URI (must match GCP config)

**Backend (FastAPI)**
- `GOOGLE_CLIENT_SECRET`: Google OAuth client secret (backend code exchange)
- `ADMIN_BOOTSTRAP_TOKEN`: admin bootstrap token for `/v1/auth/admin/claim`
- `RESEND_API_KEY`: Resend API key
- `RESEND_FROM_EMAIL`: sender address (example: `Uni API <onboarding@resend.dev>`)
- `EMAIL_VERIFICATION_REQUIRED`: `true/false`
- `EMAIL_VERIFICATION_TTL_MINUTES`: code TTL (minutes, default `10`)

**Database (docker compose)**
- `POSTGRES_USER`: database user (default `uniapi`)
- `POSTGRES_DB`: database name (default `uniapi`)

### `apps/api/.env` (only if you run the backend without Docker)

```bash
cd apps/api
cp .env.example .env
```

- `DATABASE_URL`: DB connection string (local typically `localhost:5432`)
- `APP_ENV`: `dev` / `prod`
- `APP_NAME`: backend service name
- `API_PREFIX`: API prefix (default `/v1`)
- `SESSION_TTL_DAYS`: session TTL (days)
- `SEED_DEMO_DATA`: `true/false` (optional)

## Useful Commands

- Reset DB (dangerous: deletes data):
  ```bash
  docker compose down -v
  ```
- Backup DB (example):
  ```bash
  docker compose exec -T db pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB" > backup.sql
  ```
