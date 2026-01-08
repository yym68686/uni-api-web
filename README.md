# Uni API Web (Console + Backend)

This repo hosts:
- **Console (Next.js)**: current project in the repo root (App Router).
- **Backend (FastAPI + Postgres)**: `apps/api`.

## Backend (FastAPI + Postgres)

Start Postgres + API (Docker):
```bash
docker compose up -d --build postgres api
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

Local dev (recommended): run API via Docker, run Next.js locally:
```bash
docker compose up -d --build postgres api && npm install && npm run dev
```

## CI: Build & Push Docker Images

This repo includes a GitHub Actions workflow that builds and pushes two images to Docker Hub:
- `DOCKERHUB_USERNAME/uni-api-frontend`
- `DOCKERHUB_USERNAME/uni-api-backend`

Required GitHub Secrets:
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN` (Docker Hub access token)

Tags:
- On `main`: pushes `main` + `sha-...`
- On git tag `v*` (e.g. `v0.1.0`): also pushes the tag

Set backend URL (recommended):
- `cp .env.example .env` (or set `API_BASE_URL`)

Google 登录：
- 在 Google Cloud Console 创建 OAuth Client（Web）
- Redirect URI 配置为 `http://localhost:3000/api/auth/google/callback`
- 在根目录 `.env` 填写 `GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET/GOOGLE_REDIRECT_URI`
- 重启：`docker compose up -d --build`

Next step (optional): move the console into `apps/console` and add npm workspaces if you want stricter monorepo boundaries.
