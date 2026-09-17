# uni-api console

A new React + TypeScript workspace for observing uni-api model channels. This
repository's previous frontend has been replaced; historical versions remain in
Git history.

## Run locally

```sh
npm ci
npm run dev
```

Open http://127.0.0.1:4173 and enter your uni-api public endpoint and the first
configured key (or an admin key). The backend must provide the platform APIs
listed below and allow the browser origin through CORS. HTTPS deployments require
an HTTPS backend endpoint. Verified connections are saved in the current tab's
`sessionStorage`, allowing reloads to restore the connection without another login.
The platform key is revalidated before any channel queries. Authentication rejection
clears the saved connection; a temporary network failure keeps it available for retry.
Disconnect clears the credential and query cache. Credentials are never written to
`localStorage`; normal tab closure ends their session (browser session restore may
retain session storage). Theme and filter preferences are saved locally. Filters are
stored separately for each normalized service address and restored after reconnecting.
Only the selected filter key's opaque ID is stored, never the access credential.

## Features

- Separate connection page, responsive workspace, light and dark themes.
- Provider configuration order by default; API key selection keeps its configured
  channel set and order. Search, model/status/balance filters, explicit sorting
  and pagination compose without changing routing.
- Remember API key, model, endpoint, streaming mode, time window, search, status, balance and sort filters
  across reloads. Reset filters in one click; deleted keys/models require an
  explicit new selection. Reopening starts on the first page.
- Channel success rate, completed attempt count, request-to-dispatch p50 and first
  output p50/p95. Detailed drawers show upstream model, exact latest timing,
  samples, last success and balance details.
- Balance view with independent loading, three concurrent reads, five-minute
  cache and explicit unsupported/error/unknown states. Shared balances are never
  summed. An exhausted channel requires all upstream keys to be known and empty.
- Optional one-minute traffic buckets from the timeseries API; no synthetic charts.
- Optional 30-second metric refresh, paused while the page is in the background.
- S3-backed history across today/week/month/year/all ranges, usage and estimated
  cost dashboards, live channel concurrency, cache rates and editable model prices.
- Motion entrance/tab transitions, Radix accessible dialogs/tooltips, reduced
  motion support, keyboard navigation and a mobile navigation drawer.

## Metric scope

The workspace defaults to `endpoint=all&stream=all`; endpoint and streaming mode
can be filtered independently. Aggregate latency quantiles are calculated by the
backend from merged histograms, never by averaging group p50/p95 values. Attempts are not
user requests: retries count separately. Overall success rate is weighted by
completed attempts, never averaged from row percentages. API key selection
filters channel configuration; statistics still include all requests to those
channels. p50/p95 are histogram upper-bound estimates and must not be added
between stages. Historical facts are retained in S3 and queried through DuckDB
minute/day aggregates. Only current concurrency remains a live instance metric.
Error responses never turn into a fake zero balance or success rate. The analytics API
reports each source's latest persisted fact separately from the last successful S3
scan. If a source has recent completed live traffic but no persisted fact for over
two minutes, the console warns of missing history and labels zero attempt counts
as unsynchronized. An idle source or an in-flight request alone does not trigger
this warning.

## Platform endpoints

- `GET /v1/api-keys`
- `GET /v1/model-channels`
- `GET /v1/channel-metrics`
- `GET /v1/channel-metrics/timeseries`
- `GET /v1/channel-balances?provider=...`
- `GET /analytics/v1/analytics?range=...` (analysis service)
- `GET /analytics/v1/status` (analysis service)
- `GET /analytics/v1/prices` and `PUT /analytics/v1/prices/{model}`

Users sign in with a console username and password. The API stores bcrypt password
hashes and hashed, revocable 30-day sessions in PostgreSQL. Cookies are HttpOnly,
Secure and SameSite=Strict. The frontend never stores a source credential.

Under Sources, add a name, uni-api URL and platform administrator key. Optional
S3 read credentials connect that source's historical facts. The backend encrypts
credentials with `CONTROL_MASTER_KEY`, validates platform access and proxies only
observation endpoints. The default view combines sources; selecting a source
scopes catalog, API keys, concurrency, balances and historical analytics. Same-name
providers are separate rows. Shared upstream wallet totals must not be added across
sources: these are account-level charges, not source-attributable request costs.

Set `PUBLIC_ORIGIN` to the browser-facing console origin when a hosting proxy rewrites Host. Same-origin checks use this explicit origin; they never trust a client to choose it. Nginx also forwards its received host for simple local compose deployments.
The API supports `/v1/auth/me`, `/login`, `/logout`, `/password` under `/v1/auth`,
source CRUD under `/v1/sources`, and authenticated source observation proxies.
When account mode is disabled, a legacy direct-key connection remains available.
Network failure never downgrades an enabled account service to legacy mode.

## Rebuildable analysis service

`docker-compose.yml` contains the frontend, Go analysis API and a private PostgreSQL service. Set `POSTGRES_PASSWORD`, a random `CONTROL_MASTER_KEY` (at least 32 characters), and initial `ADMIN_USERNAME` / `ADMIN_PASSWORD` (12–72 bytes). Bootstrap only creates the first administrator; change the password under Sources after signing in. Fugue may supply an independent managed PostgreSQL via `CONTROL_DATABASE_URL` instead of the compose database. Back up this database and the master key together. DuckDB remains a disposable local query cache.

Provide the
fact bucket through `S3_ENDPOINT`, `S3_BUCKET`, `S3_PREFIX` and a read-only AWS
credential. Provide a separate private state bucket through `STATE_S3_ENDPOINT`,
`STATE_S3_BUCKET`, `STATE_S3_PREFIX` and the `STATE_AWS_*` read-write credential.
Sources can use separate fact buckets/prefixes; credentials are not included in the repo.

Operator prices live in a conditional S3 document. Concurrent changes return a
conflict instead of overwriting a newer version. Every replica refreshes its price
cache. The state bucket also holds one atomically replaced, checksummed Parquet
checkpoint of facts, aggregates and import checkpoints. Settings are kept separate
so restoring an older query cache cannot revert an operator edit.

Each API container uses its own `/data` directory, with no shared volume. At startup
it restores a compatible checkpoint, replays unimported immutable facts and becomes
healthy after the initial scan. A missing or invalid checkpoint triggers a rebuild
from the raw facts. Existing replicas continue serving while replacements warm up.
Query checkpoints are derived caches; the fact and state buckets are authoritative.

## Build and verify

```sh
npm test
npm run build
npm run preview
```

Vite creates `dist/`. The Dockerfile builds with Node 24 and serves static files
with Nginx. `/healthz` returns a JSON health signal. GitHub Actions tests and builds
on push and pull requests. Dependencies are pinned through `package-lock.json`.

## Fugue source deployment

```sh
fugue --account yym68686@gmail.com --project uni-api-console \
  app create uni-api-console --github https://github.com/yym68686/uni-api-web \
  --branch main --build dockerfile --dockerfile Dockerfile --port 80
fugue --account yym68686@gmail.com --project uni-api-console app build uni-api-console
fugue --account yym68686@gmail.com --project uni-api-console app deploy uni-api-console
fugue --account yym68686@gmail.com --project uni-api-console app source sync resume uni-api-console
```

Fugue keeps `yym68686/uni-api-web` `main` as the durable source and automatically
builds/deploys changes. It is a separate app from the existing 0-0 product frontend.
No model service settings or production routing are changed by this application.

## 渠道检测

侧边栏「渠道检测」共享渠道观测的来源、API key、模型、搜索、状态、余额、排序等筛选。筛选只决定渠道集合；检测固定请求 `gpt-6-astra` 的非流式 `/v1/responses`，提问「你的知识截止到哪年哪月？只答 YYYY-MM；不确定答未知。」每个来源的渠道去重后检测一次，一键检测覆盖全部匹配页，所有筛选渠道同时发起，可取消仍在进行的检测。

只读取成功、完整 Responses 的 assistant output_text：「未知」为绿勾「不降智」，「2024-06」为红叉「降智」，同时命中或均未命中为「无法判定」。这是用户指定的回复规则，不是完整能力评测；请求失败另列。检测消耗正常模型用量。

控制台使用登录会话调用 `GET/POST /v1/sources/{id}/channel-checks`，凭据只留在服务端。最近一次结果、原始文本、时间、耗时与请求 ID 保存在 PostgreSQL 的 `console_channel_checks`，按来源与渠道隔离，重启后仍在。数据库短期租约避免同一渠道重复检测，请求期间不占用数据库连接。uni-api 须支持 `capabilities.targeted_responses`；旧版本不会发送检测请求。使用管理员渠道定向头，不会回退、重试或发起竞速请求，不改动现有路由配置。

## 临时渠道控制

账户登录后从「渠道观测」切换到「渠道控制」，直接复用同一布局、筛选状态、指标、趋势和分页；默认全部来源立即显示表格，仅多一列临时控制。每行自动对应自己的来源。上下移动和停用先形成草稿，点击行内「应用」保存当前来源＋key＋模型范围的修改；「放弃」撤销草稿，「恢复此范围」清除已生效规则。

规则只保存在 uni-api 当前进程内存，无到期时间，不写配置文件、S3 或数据库。uni-api 重启或重新部署后清空；控制台不会自动重放旧规则。多个 uni-api 运行实例之间不共享临时状态。每次保存包含进程、配置和规则版本，过期的编辑返回冲突并要求重新加载，避免覆盖他人的修改或在重启后意外重放。

顺序优先级为 key＋模型、key、模型、全局；停用规则取所有适用范围的并集，局部启用无法覆盖更广范围的停用。原有 key 权限、模型映射、端点限制、冷却和限流继续生效。已经进入的请求使用原候选列表，不强制中断。全部匹配渠道都停用时，新请求返回 503。

接口：控制台 `GET/POST /analytics/v1/sources/{id}/channel-controls`，uni-api `GET/POST /v1/channel-controls`。仅接受已登录控制台会话和来源的首个配置密钥或管理员密钥。普通业务 key 无管理权限。修改写入 uni-api 审计日志，密钥仅以不可逆 ID 标识。

渠道控制与渠道观测使用同一套搜索、来源、API key、模型、时间、端点、流式、余额、状态和排序筛选，支持 11 个时间范围。切换视图、时间和分页保留草稿。排序只在同一来源内调整，未显示的渠道仍保留；按成功率或延迟排序时禁用上下移动，避免混淆显示排序与路由优先级。路由修改的范围只由来源、API key 和模型决定，其他筛选只影响展示。
