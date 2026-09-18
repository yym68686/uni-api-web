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

渠道检测已合并到「渠道观测」，无独立检测页面。搜索框同行点击「降智检测」只展开操作，按钮变为「一键检测」「取消检测」，每行显示「重新检测」。点击「取消检测」停止未完成的批量及单行检测并隐藏操作，已完成和上次检测结果保留；顺序调整草稿不受影响。检测使用观测页的来源、API key、模型、搜索、状态、余额、排序等筛选。筛选只决定渠道集合；检测固定请求 `gpt-6-astra` 的非流式 `/v1/responses`，使用用户指定的糖果数量推理题。每个来源的渠道去重后检测一次，一键检测覆盖全部匹配页，所有筛选渠道同时发起，可取消仍在进行的检测。

只读取成功、完整 Responses 的 assistant output_text：回复包含 `21` 为绿勾「不降智」，不包含 `21` 为红叉「降智」。这是用户指定的回复规则，不是完整能力评测；请求失败另列。检测消耗正常模型用量。

控制台使用登录会话调用 `GET/POST /v1/sources/{id}/channel-checks`，凭据只留在服务端。最近一次结果、原始文本、时间、耗时与请求 ID 保存在 PostgreSQL 的 `console_channel_checks`，按来源与渠道隔离，重启后仍在。数据库短期租约避免同一渠道重复检测，请求期间不占用数据库连接。uni-api 须支持 `capabilities.targeted_responses`；旧版本不会发送检测请求。使用管理员渠道定向头，不会回退、重试或发起竞速请求，不改动现有路由配置。

## 临时渠道控制

渠道观测表格新增「是否降智」，按来源和渠道显示最近一次 gpt-6-astra 检测的结果及时间；未检测、无法判定、检测失败分别显示。结果独立于指标时间范围和所选模型，悬停可查看检测模型和回复。重新检测期间保留上次结果，页面刷新后读取已保存结果。

渠道详情右侧显示「可用模型」。通过 sub2api 添加的渠道按来源和 provider 精确关联账号、分组，显示最近检测通过的全部模型，并标注当前 API key 已添加的模型和检测时间；普通渠道显示当前 API key 配置范围内可用的模型。列表不受表格的模型与搜索筛选影响，打开详情只读取已有结果，不发起模型检测。

账户登录后，在「渠道观测」搜索框旁点击「调整顺序」，表格显示「临时控制」列，无独立渠道控制页面。点击「取消调整」会放弃全部未保存草稿并隐藏该列，已应用规则保留。编辑时直接使用原有筛选、指标、趋势和分页。每行自动对应自己的来源。上下移动和停用先形成草稿，搜索框同行的「应用」「放弃」位于「撤销更改」旁边，统一处理全部未保存草稿，包括筛选隐藏的来源、API key 和模型范围。一次应用会保存所有范围，同一来源的多个范围依次使用已确认的最新版本提交；成功后立即显示生效顺序，失败的草稿保留并提示，不覆盖外部修改。「放弃」仅清除未保存草稿；「撤销更改」清除已生效规则，悬停可查看具体来源、API key 和模型范围，不展示额外说明文字。有多个来源范围时，从按钮菜单中选择要撤销的一项；不会一次撤销其他来源。

规则在 uni-api 中仍为进程内状态，不写入原始配置文件。来源设置默认开启“保留临时配置”：控制台将已应用的临时渠道凭据、顺序和停用规则加密保存在自己的 PostgreSQL 中，在来源重启后恢复。关闭开关后不再恢复；重新开启时以当前状态为准，删除和撤销操作不会在重启后复活。恢复使用实例标识和版本校验，新实例已有不同修改时暂停并显示冲突。控制台不可达时会保留已有备份。

支持启动恢复的 uni-api 可配置 `UNI_API_CONTROL_RESTORE_URL=https://<console>/analytics/v1/runtime-restore/<source-id>` 和 `UNI_API_CONTROL_RESTORE_TOKEN=<此来源的平台管理密钥>`。新进程校验并一次性加载保存的全部配置后才监听端口；恢复服务不可用时等待重试。未配置这两个变量时仍保持独立运行；控制台后台约每 5 秒核对来源状态并恢复，因此不保证首个业务请求前恢复。平台密钥不返回浏览器，启动恢复端点只接受对应来源的管理凭据。

顺序优先级为 key＋模型、key、模型、全局；停用规则取所有适用范围的并集，局部启用无法覆盖更广范围的停用。原有 key 权限、模型映射、端点限制、冷却和限流继续生效。已经进入的请求使用原候选列表，不强制中断。全部匹配渠道都停用时，新请求返回 503。

接口：控制台 `GET/POST /analytics/v1/sources/{id}/channel-controls`，uni-api `GET/POST /v1/channel-controls`。仅接受已登录控制台会话和来源的首个配置密钥或管理员密钥。普通业务 key 无管理权限。修改写入 uni-api 审计日志，密钥仅以不可逆 ID 标识。

渠道观测与顺序调整使用同一套搜索、来源、API key、模型、时间、端点、流式、余额、状态和排序筛选，支持 11 个时间范围。切换视图、时间和分页保留草稿。排序只在同一来源内调整，未显示的渠道仍保留；按成功率或延迟排序时禁用上下移动，避免混淆显示排序与路由优先级。路由修改的范围只由来源、API key 和模型决定，其他筛选只影响展示。

### sub2api 检测

账户登录后，侧边栏「sub2api检测」支持添加多个 HTTPS 站点账号。邮箱、密码用于登录，不保存密码；访问令牌、刷新令牌与测试 key 使用现有控制面主密钥加密，按控制台用户隔离。支持 TOTP 二次验证。要求 Turnstile 等人机验证的站点会显示「使用浏览器登录」；一次性安装 [浏览器登录助手](browser-helper/README.md) 后，在控制台填写网址、邮箱和密码，助手在正常浏览器打开原站、填写表单并接回会话，服务端复核邮箱后继续同步。添加和重新登录在模态框内完成，按用户默认授权自动勾选原站登录协议，强制人工验证和 2FA 在原站完成，无需复制 token。密码不保存到扩展存储，验证机制不被修改。登录助手安装时授予 HTTPS 站点访问权限，点击浏览器登录直接进入目标站点。

添加后自动读取有权限的全部分组，为每组创建并复用 `uni-console-check-*` 专用 key（累计额度为站点记账单位 $1），不修改业务 key。重复同步通过专用名称、分组校验与 Idempotency-Key 避免重复创建。可用渠道接口关闭时继续按分组运行。移除账号只删除控制台凭据与结果，上游测试 key 保留，由用户在上游撤销。

检测模型为 `gpt-6-astra`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`、`gpt-5.5`、`codex-auto-review`。每个模型通过 `/v1/responses` 发送流式 `say test`，从检测服务发起 HTTP 到首个 `response.created` 记录默认首字延迟，tooltip 另列到首个非空 `response.output_text.delta` 的耗时；要求成功完成事件及最终 assistant 文本，心跳、reasoning、HTTP 200 或截断连接不算成功。仅 `gpt-6-astra` 可用性成功后再次发送糖果数量推理题，其他模型不发送降智请求：最终回复包含 `21` 判为不降智，否则判为降智。可用性与此启发式规则分别显示，结果代表所选分组，不能确认其全部内部账号或模型身份。

后台 PostgreSQL 队列保存同步任务、检测进度、最后结果，网页刷新不影响任务。所有已排队的站点账号可同时同步和检测，不设账号间并发上限；每个账号仍先完成同步，再以最多两个“分组 × 模型”任务并发检测。会话互斥通过短期数据库租约实现，等待上游期间不占用数据库连接；账号租约及任务 ID 阻止重复执行与过期结果覆盖。进程中断的付费请求不会自动重放，标记中断后可手动重试。支持账号、搜索、模型、可用性、降智结果、倍率上限筛选和倍率升降序；倍率选项来自其他筛选后的数据，选择后保留小于等于阈值的渠道。表格每个站点分组只显示一行；全部模型模式隐藏模型列，汇总可用/失败/未检测数量，首字延迟主单元格显示缺失，可展开各模型结果。选择具体模型时显示该模型结果。可用性在全部模型下按“存在匹配模型”筛选分组，降智筛选始终使用分组的 Astra 结果。一键检测覆盖所有匹配页，全部模型模式为每个匹配分组提交六个模型（包含未检测和此前失败的模型）；具体模型模式只提交所选模型。按钮明确显示模型范围与渠道数量。新接口为 `/v1/sub2api/accounts`、`/v1/sub2api/accounts/{id}/sync`、`/v1/sub2api/accounts/{id}/stop`、`/v1/sub2api/checks`，仅账户会话可访问。

倍率优先读取专用 key 的 `/v1/sub2api/billing` 有效倍率（包含上游用户专属与高峰规则）。旧版不支持时，只有用户倍率接口成功且未启用高峰倍率才使用分组/专属倍率；不能确定则显示缺失。界面只显示上游数字，不做汇率换算，悬停可见同步时间。

渠道观测与单次检测的默认首字延迟均使用首个 `response.created` 的耗时，tooltip 显示首个 `response.output_text.delta` 的延迟。两个事件分别采集，未采集创建事件的历史、非流式或不包含该事件的请求显示缺失，不以旧首输出值填充。p50 与单次检测延迟使用统一 badge：≤5 秒绿色、>5 秒且≤10 秒黄色、>10 秒红色；缺失值、请求前等待、p95 保持原样。

「添加到渠道」弹窗默认勾选该分组最新检测成功的模型，选择 uni-api 来源、目标 API key 和从 1 开始的位置。仅允许添加已测可用模型；由后端创建并复用独立 `uni-console-route-*` 业务 key（采用上游默认额度），加密保存并直接传到网关，浏览器不接触凭据。不会把 $1 检测 key 用于生产调用。

目标 uni-api 必须支持 `temporary_channel_import` 能力。添加携带弹窗读取的控制 revision，冲突不自动覆盖。网关原子添加临时 provider 与各模型的优先顺序，严格限定目标 key；其他 key 的 `all` 或嵌套引用不扩大访问。添加到第 1 位即优先尝试，仍遵守现有禁用/冷却/请求限制。重启清空，或在渠道观测的顺序调整中撤销相应范围。重复添加同一账号/分组/目标 key 更新现有临时渠道，不重复建渠道。

搜索框旁的「降智检测」覆盖当前筛选范围内所有分页的可检测渠道，固定每个分组只发送一次 `gpt-6-astra` 糖果数量推理题请求，不发送 `say test`，也不检测其他模型。通过独立的 `/v1/sub2api/quality-checks` 接口加入持久队列，可在账号卡片停止；刷新页面后继续显示进度和结果。此次响应同时更新 Astra 的降智结果、可用性、首字延迟和模型匹配，其他模型结果保持不变。

sub2api 检测的「模型匹配」比较请求模型与成功流式 `response.completed` 对象中的 `response.model`，模型名须完全一致（不合并别名、版本后缀、大小写）。常规检测使用 `say test` 的响应；独立降智检测使用此次降智请求的响应，无额外上游请求。结果记录请求模型、返回模型与判定：匹配、不匹配、未返回、格式无效或无法判定。初始事件及输出文本不作为模型身份，模型匹配也不自动改变路由。全部模型视图汇总数量，展开可看各模型详情；历史记录未采集该字段时显示「待补测」。

站点账号卡片通过独立余额接口读取上游 `/api/v1/auth/me` 的钱包余额，按账号缓存 5 分钟，查询失败保留上次值并标注。它不会触发模型检测；刷新登录会话与分组同步串行处理，避免重复轮换 token。所有余额金额共用颜色：小于 0 沿用负值红色，0 至 50（含边界）黄色，大于 50 绿色；未知金额不按 0 处理。
