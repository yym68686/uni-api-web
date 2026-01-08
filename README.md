# Uni API Web (Console + Backend)

This repo hosts:
- **Console (Next.js)**: current project in the repo root (App Router).
- **Backend (FastAPI + Postgres)**: `apps/api`.

## Backend (FastAPI + Postgres)

Start Postgres:
```bash
docker compose up -d
```

This also starts the FastAPI service on `http://localhost:8001`.

Run API without Docker:
```bash
cd apps/api
python -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
cp .env.example .env
uvicorn app.main:app --reload --port 8001
```

Health check: `GET http://localhost:8001/v1/health`

## Console (Next.js)

```bash
npm install
npm run dev
```

Set backend URL (recommended):
- `cp .env.example .env` (or set `API_BASE_URL`)

Next step (optional): move the console into `apps/console` and add npm workspaces if you want stricter monorepo boundaries.
