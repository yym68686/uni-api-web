# Uni API Web

[英文](./README.md) | [中文](./README_CN.md)

Uni API Web 是一个完整的 **LLM API 控制台 + 网关后端**：账号体系、API Key 管理、模型列表与价格、请求日志、管理员配置（渠道/模型/用户）、公告等。

本仓库包含：
- **Console (Next.js 16 + TypeScript + Tailwind v4 + shadcn/ui)**：仓库根目录
- **Backend (FastAPI + Postgres)**：`apps/api`

## 主要特性

**用户侧**
- 落地页：深色氛围 + 毛玻璃 + 微交互
- 登录/注册：邮箱密码 + 邮箱验证码（Resend）+ Google OAuth（PKCE）
- Dashboard：用量/消费趋势、剩余余额、公告列表（服务端数据）
- 密钥：一键创建（自动命名），可随时重命名；撤销/恢复/删除；默认掩码；**随时可复制完整密钥**；显示上次使用与总消费
- 模型：展示当前账号可用模型与输入/输出价格（按 $/M tokens 计费；列表显示为 `$X`）
- 日志：每次请求的模型、时间、输入/输出 token、总时长、首字时长、TPS、花费、来源 IP
- 账单：余额 + 余额变更历史（当前仅包含管理员调整）
- Profile：账号信息 + 注销账号

**管理员侧**
- 渠道（Channels）：配置上游 Base URL + API key；可限制哪些“分组”可以使用
- 模型配置（Model Config）：从渠道聚合 `/v1/models` 去重后启用/禁用模型并设置价格；支持手动刷新
- 用户管理（Users）：封禁/解封、删除、修改余额、修改角色/分组（Owner 保护）
- 公告（Announcements）：发布/编辑/删除；Dashboard 展示时间与内容

**体验/性能**
- 控制台与管理端页面统一 “先骨架屏，再流式填充内容”（`loading.tsx` + `Suspense`）
- 路由切换轻量过渡（CSS-only，支持 `prefers-reduced-motion`）
- i18n：简体中文/英文，自动根据浏览器语言写入 `uai_locale` cookie

## 项目结构

```text
.
├─ src/                 # Next.js Console
├─ src/proxy.ts         # Next.js Proxy：鉴权门禁 + /v1/* 反向代理到后端
├─ apps/api/            # FastAPI Backend
├─ docker-compose.yml   # 本地/部署（db + api + 可选 web）
└─ .github/workflows/   # CI：构建并推送 Docker 镜像
```

## 本地开发（推荐）

1) 准备环境变量：
```bash
cp .env.example .env
```
至少需要设置：
- `POSTGRES_PASSWORD`（docker compose 会强制要求）
- 如需登录能力：`GOOGLE_*` / `RESEND_*`

2) 启动后端（Postgres + API）：
```bash
docker compose up -d --build db api
```
后端地址：`http://localhost:8001`（对外端口），健康检查：`GET http://localhost:8001/v1/health`

3) 启动前端（Next.js dev）：
```bash
npm install
npm run dev
```
前端地址：`http://localhost:3000`

一条命令（常用）：
```bash
docker compose up -d --build db api && npm install && npm run dev
```

## 全部 Docker 化运行（可用于服务器）

```bash
docker compose up -d --build
```

- 前端容器：`http://localhost:3000`
- 后端容器：宿主机 `http://localhost:8001` → 容器内实际监听 `8000`
- `/v1/*` 会由 Next.js Proxy 转发到 `API_BASE_URL`

## 管理员（Owner/Admin）机制

- **空数据库首次注册的用户**会自动成为 **Owner**（最高权限）。
- 后续如需授予某个用户管理员权限：
  1) 设置后端环境变量 `ADMIN_BOOTSTRAP_TOKEN`（强随机，勿泄露）
  2) 该用户登录后调用：
     ```bash
     curl -X POST 'http://localhost:8001/v1/auth/admin/claim' \
       -H 'Content-Type: application/json' \
       -H "Authorization: Bearer <SESSION_TOKEN>" \
       -d '{"token":"<ADMIN_BOOTSTRAP_TOKEN>"}'
     ```

## Google OAuth（Web Client）配置步骤

1) 打开 Google Cloud Console → APIs & Services → Credentials
2) Create credentials → OAuth client ID → Application type 选 **Web application**
3) 配置 **Authorized redirect URIs**（必须与 `GOOGLE_REDIRECT_URI` 完全一致）：
   - 本地：`http://localhost:3000/api/auth/google/callback`
   - 线上示例：`https://<your-domain>/api/auth/google/callback`
4) 拿到 `Client ID` / `Client secret`，填入环境变量：
   - 前端（Next）：需要 `GOOGLE_CLIENT_ID`、`GOOGLE_REDIRECT_URI`
   - 后端（FastAPI）：需要 `GOOGLE_CLIENT_ID`、`GOOGLE_CLIENT_SECRET`、`GOOGLE_REDIRECT_URI`

## 邮箱验证码（Resend）

- 设置 `RESEND_API_KEY` 与 `RESEND_FROM_EMAIL`
- `EMAIL_VERIFICATION_REQUIRED=true` 时注册/登录会要求邮箱验证码
- `EMAIL_VERIFICATION_TTL_MINUTES` 调整验证码有效期（默认 `10` 分钟）

## 部署（镜像 + Docker Hub）

GitHub Actions 会构建并推送两个镜像：
- `DOCKERHUB_USERNAME/uni-api-frontend`
- `DOCKERHUB_USERNAME/uni-api-backend`

工作流：`.github/workflows/docker-build-push.yml`
需要的 GitHub Secrets：
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`（Docker Hub Access Token，需包含 push 权限）

**A) 服务器从源码构建（直接使用本仓库 `docker-compose.yml`）**
```bash
docker compose up -d --build
```

**B) 服务器使用 Docker Hub 镜像（推荐：更快、更可控）**

创建一个仅拉取镜像的 compose（示例 `docker-compose.prod.yml`）：
```yaml
services:
  web:
    image: DOCKERHUB_USERNAME/uni-api-frontend:main
    restart: unless-stopped
    depends_on: [api]
    environment:
      API_BASE_URL: http://api:8000/v1
      PUBLIC_API_BASE_URL: ${PUBLIC_API_BASE_URL:-}
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

更新流程：
```bash
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
```

安全说明：
- 不要把密钥写进镜像；应使用运行时环境变量注入。
- 不要把任何密钥放入 `NEXT_PUBLIC_*`（会暴露到浏览器端）。

## 环境变量说明（全部）

### 根目录 `.env`（Next + docker compose 共用）

**必填（docker compose）**
- `POSTGRES_PASSWORD`：Postgres 密码（必须强密码）

**前端 / Proxy（Next.js）**
- `API_BASE_URL`：后端 API 基地址（默认 `http://localhost:8001/v1`），用于：
  - Next.js Server 侧请求后端（`src/lib/backend.ts`）
  - `src/proxy.ts` 将 `/v1/*` rewrite 到该地址
- `PUBLIC_API_BASE_URL`：对用户展示的 API Base URL（显示在「密钥」页面顶部，可一键复制；例如 `https://api.0-0.pro/v1`）
- `APP_NAME`：站点名称（默认 `MyApp`）
- `GOOGLE_CLIENT_ID`：Google OAuth Client ID
- `GOOGLE_REDIRECT_URI`：OAuth 回调地址（必须与 GCP 配置一致）

**后端（FastAPI）**
- `GOOGLE_CLIENT_SECRET`：Google OAuth Client Secret（后端交换 code 使用）
- `ADMIN_BOOTSTRAP_TOKEN`：管理员引导 token（用于 `/v1/auth/admin/claim`）
- `RESEND_API_KEY`：Resend API key（发送验证码邮件）
- `RESEND_FROM_EMAIL`：发件人（例如 `Uni API <onboarding@resend.dev>`）
- `EMAIL_VERIFICATION_REQUIRED`：是否强制邮箱验证码（`true/false`）
- `EMAIL_VERIFICATION_TTL_MINUTES`：验证码有效期（分钟，默认 `10`）

**docker compose（数据库）**
- `POSTGRES_USER`：数据库用户（默认 `uniapi`）
- `POSTGRES_DB`：数据库名（默认 `uniapi`）

### `apps/api/.env`（仅当你不使用 Docker 运行后端）

```bash
cd apps/api
cp .env.example .env
```

- `DATABASE_URL`：数据库连接串（本地通常是 `localhost:5432`）
- `APP_ENV`：运行环境（`dev` / `prod`）
- `APP_NAME`：后端服务名
- `API_PREFIX`：API 前缀（默认 `/v1`）
- `SESSION_TTL_DAYS`：登录会话有效期（天）
- `SEED_DEMO_DATA`：是否写入演示数据（可选）

## 常用命令

- 重置数据库（清空数据，慎用）：
  ```bash
  docker compose down -v
  ```
- 备份数据库（示例）：
  ```bash
  docker compose exec -T db pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB" > backup.sql
  ```
