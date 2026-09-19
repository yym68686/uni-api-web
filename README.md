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

Each API container uses its own `/data` directory, with no shared volume.
Configuration recovery, detection and billing workers start before history
restoration. Production uses `REQUIRE_INITIAL_IMPORT=true`: the replacement does
not bind its HTTP port until a complete initial fact scan and price sync finish.
With Fugue's TCP readiness and `maxUnavailable=0`, the old replica continues
serving login, control and analytics until the replacement is ready. This traffic
gate does not delay background workers or optional query warming.

`/healthz` reports process health once listening; `/readyz` reports complete
historical analytics readiness. In immediate-listen/development mode, incomplete
analytics returns 503 with `code: analytics_initializing` and `Retry-After: 5`.
The console retries automatically even with auto-refresh disabled, retaining the
last successful data for the same query and labeling it as previous data. It never
substitutes another key/range's cached result. Initialization does not display an
unrelated S3 export-configuration warning. `/v1/prices` waits only for price sync.

After initialization, each source polls independently, with at most four scans
and sixteen object downloads in flight across the service. Newly discovered
objects are imported before the next listing page; every page is still scanned
so late arrivals cannot be skipped. Fact/object checkpoints remain atomic and
idempotent. A large source archive does not delay another source's next poll.
Analytics import diagnostics are scoped to the selected source and include
`scanning`, scan start/completion, pages, listed objects and known pending objects.
An active scan with zero discovered pending objects is not reported as caught up.
Freshness warnings describe import lag without asserting an S3 export failure.

Checkpoint restoration retries transient storage, timeout and interrupted-download
failures at most three times, with a five-minute deadline per attempt and backoff
of two then four seconds. Missing, incompatible, corrupt or access-denied snapshots
fall back directly to raw facts. Restoration applies all history tables atomically;
failed attempts preserve the existing cache and never restore operator settings.
Logs report operation, stage, error class, attempt, duration and retry decision,
without raw SDK errors, URLs or credentials. `/v1/status` exposes analytics readiness,
startup phase and the last restore failure. Shutdown cancels restoration/backoff
and drains import, checkpoint and worker goroutines before closing databases.
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

支持 22 个检测模型：`gpt-6-astra`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`、`gpt-5.5`、`codex-auto-review`、`glm-5.3`、`glm-5.3-flash`、`kimi-k3`、`deepseek-4.1-flash`、`deepseek-4-pro`、`grok-4.6`、`gemini-3.1-pro`、`gemini-3.8-flash`、`claude-fable-5`、`claude-fable-5-1`、`claude-opus-5`、`claude-sonnet-5`、`claude-opus-4-8`、`claude-opus-4-6`、`claude-sonnet-4-6`、`claude-haiku-4-5-20251001`。

每个模型发送流式 `say test`。Claude 使用 `/v1/messages` 和 `x-api-key`，两个 Gemini 使用 `/v1beta/models/{model}:streamGenerateContent?alt=sse` 和 `x-goog-api-key`，其他使用 `/v1/responses`。不尝试切换协议或回退模型。Responses 默认延迟记录首个 `response.created`，Claude 记录首个 `message_start`，Gemini 记录首个原生响应事件；tooltip 同时列出首个非思考文本片段的延迟，原生事件不会伪装为 `response.created`。模型匹配使用原生返回的 `model` / `modelVersion`，缺失时不拿请求模型补全。请求 ID 继续用于账单核验。完整 Responses、正常结束的 Messages、以 STOP 完成的 Gemini 且有可见文本才判可用；思考内容、HTTP 200 和截断流不算成功。仅 `gpt-6-astra` 可用性成功后再次发送糖果推理题，回复含 `21` 判不降智，否则判降智；其他模型只发送 `say test`。

后台 PostgreSQL 队列保存同步任务、检测进度、最后结果，网页刷新不影响任务。所有已排队的站点账号可同时同步和检测，不设账号间并发上限；每个账号仍先完成同步，再以最多两个“分组 × 模型”任务并发检测。会话互斥通过短期数据库租约实现，等待上游期间不占用数据库连接；账号租约及任务 ID 阻止重复执行与过期结果覆盖。进程中断的付费请求不会自动重放，标记中断后可手动重试。支持账号、搜索、模型、可用性、降智结果、倍率上限筛选和倍率升降序；倍率选项来自其他筛选后的数据，选择后保留小于等于阈值的渠道。表格每个站点分组只显示一行；全部模型模式隐藏模型列，汇总可用/失败/未检测数量，首字延迟主单元格显示缺失，可展开各模型结果。选择具体模型时显示该模型结果。可用性在全部模型下按“存在匹配模型”筛选分组，降智筛选始终使用分组的 Astra 结果。一键检测覆盖所有匹配页，“检测全部模型”旁的设置弹窗默认勾选全部模型；保存后按控制台用户写入浏览器本地存储，刷新及返回页面保留选择。设置用于模型检测区域的批量和逐渠道检测，包含未检测和此前失败的已勾选模型；具体模型筛选与设置取交集，未勾选时禁用检测。至少保留一个模型，取消不保存草稿。站点账号同步仍自动检测全部支持模型，单独降智检测仍只请求 Astra。按钮明确显示模型范围与渠道数量。新接口为 `/v1/sub2api/accounts`、`/v1/sub2api/accounts/{id}/sync`、`/v1/sub2api/accounts/{id}/stop`、`/v1/sub2api/checks`，仅账户会话可访问。

倍率优先读取专用 key 的 `/v1/sub2api/billing` 有效倍率（包含上游用户专属与高峰规则）。旧版不支持时，只有用户倍率接口成功且未启用高峰倍率才使用分组/专属倍率；不能确定则显示缺失。界面只显示上游数字，不做汇率换算，悬停可见同步时间。

渠道观测与单次检测的默认首字延迟均使用首个 `response.created` 的耗时，tooltip 显示首个 `response.output_text.delta` 的延迟。两个事件分别采集，未采集创建事件的历史、非流式或不包含该事件的请求显示缺失，不以旧首输出值填充。p50 与单次检测延迟使用统一 badge：≤5 秒绿色、>5 秒且≤10 秒黄色、>10 秒红色；缺失值、请求前等待、p95 保持原样。

「添加到渠道」弹窗默认勾选该分组最新检测成功且没有已确认单价异常的模型；异常模型默认不勾选并显示提示，仍可由用户手动选择。选择 uni-api 来源、目标 API key 和从 1 开始的位置。仅允许添加已测可用模型；由后端创建并复用独立 `uni-console-route-*` 业务 key（采用上游默认额度），加密保存并直接传到网关，浏览器不接触凭据。不会把 $1 检测 key 用于生产调用。

目标 uni-api 必须支持 `temporary_channel_import` 能力。添加携带弹窗读取的控制 revision，冲突不自动覆盖。网关原子添加临时 provider 与各模型的优先顺序，严格限定目标 key；其他 key 的 `all` 或嵌套引用不扩大访问。添加到第 1 位即优先尝试，仍遵守现有禁用/冷却/请求限制。重启清空，或在渠道观测的顺序调整中撤销相应范围。重复添加同一账号/分组/目标 key 更新现有临时渠道，不重复建渠道。

搜索框旁的「降智检测」覆盖当前筛选范围内所有分页的可检测渠道，固定每个分组只发送一次 `gpt-6-astra` 糖果数量推理题请求，不发送 `say test`，也不检测其他模型。通过独立的 `/v1/sub2api/quality-checks` 接口加入持久队列，可在账号卡片停止；刷新页面后继续显示进度和结果。此次响应同时更新 Astra 的降智结果、可用性、首字延迟和模型匹配，其他模型结果保持不变。

sub2api 检测的「模型匹配」比较请求模型与成功流式 `response.completed` 对象中的 `response.model`，模型名须完全一致（不合并别名、版本后缀、大小写）。常规检测使用 `say test` 的响应；独立降智检测使用此次降智请求的响应，无额外上游请求。结果记录请求模型、返回模型与判定：匹配、不匹配、未返回、格式无效或无法判定。初始事件及输出文本不作为模型身份，模型匹配也不自动改变路由。全部模型视图汇总数量，展开可看各模型详情；历史记录未采集该字段时显示「待补测」。

站点账号卡片通过独立余额接口读取上游 `/api/v1/auth/me` 的钱包余额，按账号缓存 5 分钟，查询失败保留上次值并标注。它不会触发模型检测；刷新登录会话与分组同步串行处理，避免重复轮换 token。所有余额金额共用颜色：小于 0 沿用负值红色，0 至 50（含边界）黄色，大于 50 绿色；未知金额不按 0 处理。


每次 sub2api `say test` 与降智检测均记录独立请求标识，并在 PostgreSQL `console_sub_usage` 中排队查询该账号的 `/api/v1/usage`。账单查询仅匹配测试 Key、分组和精确请求 ID；不使用余额差、时间相邻请求或模型名猜测费用。普通 Key 不用于面板鉴权，复用加密登录会话，401 时通过现有账号租约串行刷新 token。每个账号独立租约、每批最多 32 条同 Key 请求、最多 5 页，单批 20 秒；账单延迟或暂时故障最多查询 6 次并退避，重启后继续免费查询，不重发模型请求，也不占用同站点的两个模型检测 worker。

「回复 / 诊断」详情展示每次请求的实际计费金额、倍率前与实付输入／输出单价、计费倍率、token 和对应账单 ID。单价统一为美元／百万 token，由该条账单的分项费用与对应 token 样本计算；零 token 或缺失字段保持未知，按次或图片计费不冒充 token 单价。若实际金额与总价乘倍率不一致，不推算实付分项单价。独立降智检测与可用性共用一个请求，只生成一条账单核验记录。

模型检测表格新增「单价异常」，按倍率前输入／输出单价与当前已确认的「价格设置」比较，正常分组折扣不触发异常；考虑上游费用字段的十位小数舍入误差。异常下方以 `$5/$30` 的形式显示输入／输出单价。无账单、无参考价格、零 token 或站点不支持查询时明确显示未确认，不能当作正常或免费。老检测结果没有请求关联标识，需要重新检测才能核验账单。


渠道观测的「渠道实际消费」通过 uni-api 的独立 `billing` 事实关联 sub2api 账单。网关为每次实际发出的尝试保存响应头 `X-Client-Request-ID`、上游站点、实际使用的 Key 的 SHA-256 指纹及原有来源、调用 Key、渠道、模型、端点、流式和重试维度；不保存明文 Key、请求体或回复体。控制台按登录账号的 Key 索引匹配站点和指纹，随后精确匹配 `/api/v1/usage` 的 `client:<标识>`，累计 `actual_cost`。`/v1/alpha/search` 等已知端点归一为同一站点，查询兼容旧快照中的搜索端点基址，同时保留租户路径前缀。

账单和请求标识缓存在独立 PostgreSQL 中，以账号、业务 Key、日志 ID 去重。同步以站点账号为单位，不带 `api_key_id` 或 `group_id` 过滤，一次分页下载该账号所需时间范围的所有 Key 日志，按日志自带的 Key ID 缓存，再本地批量匹配请求。默认每页 1000 条；站点限制页大小时沿用其返回值，响应超过大小限制或大页被拒绝时回退 100 条并从第一页重扫去重，绝不混用不同页大小的偏移。整页批量写入与游标提交同一事务；每批最多 5 页、20 秒，账号之间并行，同账号继续复用账单读取租约、退避与持久断点。不重发模型请求。`GET /v1/channel-spend` 使用统计接口相同的 `[from,to)`，支持来源、调用 Key、模型、端点和流式筛选；有尝试完成事实时使用其完成时间，取消的 hedge 没有完成事实时使用关联记录时间。跨来源/调用 Key 共用同一业务 Key 的账单仍只查询一次，但金额逐请求分别归属；重复响应标识、重复账单、旧请求没有标识或未绑定账号均不冒充完整消费。仅所有尝试均唯一匹配时显示完整金额，未匹配账单不推断为免费。业务 Key 汇总接口继续作为独立参考，不参与渠道利润。 部分匹配时表格显示 `≥$已核对金额` 和已核对数量，完整金额字段仍为空，利润保持未知。缺少历史事实与账单仍在同步分别表达；即使关闭自动刷新，只要还有可关联的待核对账单仍继续轮询。tooltip 分别列出缺少关联事实、缺少响应标识及 HTTP 状态、账号未绑定、冲突和待核对次数，无记录的渠道显示 `—`。


渠道观测的「利润」列按指定数值公式计算：`估算消费 × 对应模型售卖百分比 ÷ 100 × 6.9 − 渠道实际消费`，不对实际消费另做币种换算。第一行显示人民币 `¥金额`，第二行利润率为 `利润 ÷（估算消费 × 对应模型售卖百分比 ÷ 100 × 6.9）`。正利润绿色、负利润红色；缺少任一消费值时显示缺失，收入为零时利润率不可计算。利润只使用当前筛选范围完整匹配的逐请求账单。业务 Key 总额、按日金额、部分账单和无法归属的旧历史均不用于计算利润。

### 配置文件渠道的站点账号关联

后台读取 uni-api 原始渠道配置，以规范化的站点地址和完整 API key 的 SHA-256 指纹，与当前控制台用户已保存的 sub2api 账号密钥列表精确核对。识别结果、上游 Key ID、分组和创建时间保存在 PostgreSQL；页面刷新和服务重启不会重新扫描站点密钥。来源配置每分钟检查一次；只有站点下的配置 key 集合变化或用户重新同步账号时，才重新读取该账号的密钥列表。失败扫描有重试间隔，完整分页成功后原子替换索引，不保存额外的明文 key，不创建或修改上游 key。

已完整匹配的原始渠道可读取账号余额，复用关联分组的可用模型与降智检测历史，并通过实际发出请求的 Key 指纹参与逐请求账单关联。账号、站点或 Key 匹配不完整/有歧义时不混用数据，抽屉显示关联状态。配置渠道和导入渠道使用相同的精确消费口径。

配置文件渠道保持原名及原有配置，不获得临时渠道的编辑/删除入口；自动关联不修改 uni-api 路由、来源密钥或站点业务密钥。读取上游账单继续复用站点登录会话和现有自动刷新机制。


价格设置可为每个模型保存 `sale_percent`（原价的百分比）。GPT 默认 2.5%，Claude / Gemini 默认 15%，其他模型默认 2.5%；支持小数、0% 和高于 100% 的比例。后缀模型沿用最长匹配的基础模型设置。原价估算消费保持原口径，利润和利润率使用该模型的当前售卖比例；保存后会刷新渠道统计。配置与现有价格一起保存至 S3，旧价格文档省略此字段时使用默认值；旧客户端省略该字段更新价格时保留已保存比例。

发布顺序：先更新 analytics-api 消费者以支持 additive `billing` 事实，再更新网关生产者。账单事实不进入请求/尝试/token 汇总；DuckDB 快照使用 cache-v3，兼容读取 cache-v2，避免旧消费者恢复新列快照。升级前历史缺少上游标识，无法事后精确拆分。

账号级账单同步状态存入 `console_sub_account_spend_cache`，多个 Key/渠道/筛选窗口共用覆盖范围。升级只从旧 Key 游标合并需要同步的时间范围，不能把任一 Key 的完整覆盖当成账号完整覆盖；旧明细及游标表保留以兼容滚动更新。后台 `sub_account_spend_page` 日志记录账号、分页大小、返回行数及完成状态，不记录令牌或请求内容。

账单归属的完整性与下载进度分开表示：`pending_attempts` 表示账单同步尚未覆盖/查询失败的待核对请求；`absent_receipt_attempts` 表示已完成覆盖请求起止时间的账单扫描，但没有找到精确标识的记录。返回这类记录的 HTTP 状态计数和最近完整同步时间，并使用同一个 PostgreSQL repeatable-read 只读事务读取账单与覆盖范围。后者每分钟低频复查，延迟入账后自动恢复；没有账单不能据此认定免费，缺少响应标识也不能因下载完成而当作零。`≥` 只表示已核对费用下界，不承诺等待后一定变为精确总额。
