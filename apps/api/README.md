# Uni API Backend (FastAPI + Postgres)

## Local Postgres
From repo root:

```bash
docker compose up -d
```

This starts:
- Postgres on `localhost:5432`
- FastAPI on `localhost:8001` (`/v1/*`)

## Run API

```bash
cd apps/api
python -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
cp .env.example .env
uvicorn app.main:app --reload --port 8001
```

Endpoints:
- `GET /v1/health`
- `POST /v1/auth/register`
- `POST /v1/auth/login`
- `POST /v1/auth/logout`
- `GET /v1/auth/me`
- `POST /v1/auth/admin/claim`
- `POST /v1/auth/oauth/google`
- `GET /v1/keys`
- `POST /v1/keys`
- `DELETE /v1/keys/{key_id}`

Notes:
- Keys are stored in Postgres (SQLAlchemy async); only a masked `prefix` is returned in list endpoints.
- The full key is returned only once on creation (store keeps only `sha256` hash).
- Dev convenience: tables are auto-created on startup; for production use migrations (e.g. Alembic).
- Demo announcements seeding is disabled by default; set `SEED_DEMO_DATA=true` if you want sample rows.
- Admin:
  - First registered user is automatically `admin`.
  - To promote an existing user later, set `ADMIN_BOOTSTRAP_TOKEN` and call `POST /v1/auth/admin/claim` with `{ "token": "..." }`.
