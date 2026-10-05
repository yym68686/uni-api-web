# Analytics 内存优化实施与验证（2026-10-05）

## 范围与结论

本次修改 Go analytics-api：账单 SQL、请求并发、事实解码、无效 rollup 重建、快照临时文件缓存和内存诊断。没有修改模型价格、路由或原始事实格式，没有删除历史数据/索引，也没有迁移生产数据库。测试网络仅连接本机隔离 PostgreSQL；内存实验容器禁用网络。

线上历史 OOM 的精确事实是 DuckDB 的 1 GiB 内部预算耗尽；现有日志不能还原当时哪个算子占用了多少内存。本次改动依据离线热点测量，不把实验中的改善当作对那次事故的完整归因。

**当前已验证内存和查询耗时改善，但未达到或证明整个生产实例峰值降至 1/10。** 不因单查询实验成功而降低线上预算。持久化窄事实表、索引迁移、独立 worker 均未进入本次变更：它们涉及回填、回滚兼容与跨进程总内存，需要另行压测，不能把搬迁内存记为减少内存。

## 已实施

| 位置 | 原行为 | 新行为及复杂度变化 | 风险与保障 |
| --- | --- | --- | --- |
| `channel_spend.go` / `scopedBillingAlignment` | 按来源/模型筛选后物化历史 billing 的所有字段 | 只投影账单字段；通过“观察时间在窗内 OR 有窗内完成记录”缩小候选集。扫描仍与历史规模有关，但物化由全历史宽行变成候选窄行 | 最后仍检查所有历史 completion 的 max/count。保留窗口外完成、晚到记录、重复 caller ID、SQL NULL 语义 |
| `channel_spend.go` / `billingClaimsSQL` | 每 5,000 行 ledger 页扫描全历史账单认领者 | 每请求建立一次全局冲突计数，随后只查询临时结果。历史扫描由 O(P×B) 降到 O(B)，P 为页数，B 为历史账单量 | 跨来源、跨 Key、跨时间窗仍参与冲突检测；同一 event 内重复 receipt 只计一次 |
| `database_resources.go` / `analytics_spend.go` | 多请求及单页面 metrics/spend 可并发执行重计划 | metrics 与 spend 共用一个可取消的槽位；单页面顺序执行 | 继续使用原请求 deadline；排队可能增加延迟。导入、控制操作、请求追踪不占这个槽位 |
| `engine.go` / `ImportBatch` | trace/billing 导入也重建整分钟和整天统计 | 不参与 rollup 的记录不触发 rollup 重建 | 原始事实和导入标记仍在同一事务提交；混合批次中的 metric 记录正常重建 |
| `s3_import.go` / `decodeFacts` | 原始对象整份 byte、string 和解码结果同时存在 | Scanner 逐行解码，去掉两份整对象缓冲，保留解码结果和不超过 1 MiB 的行缓冲 | 保留 32 MiB 对象/10,000 事件限制；截断、非法尾行、过大对象整体拒绝，不提交部分数据 |
| `checkpoint_cache*.go` | 私有快照副本和 gzip 文件可能留在文件缓存 | 可选分段 fsync 后 DONTNEED；读完的私有文件分段回收 | 默认关闭。仅作用于本进程 scratch 文件，绝不对活动 DB 或系统执行 drop_caches；保持 Seek 重试、哈希与原子条件发布 |
| `database_resources.go` | 只有 OOM 类别，缺少阶段与内存构成 | 每 5 秒记录操作阶段、排队数、连接池、Go heap、RSS、cgroup anon/file/peak、DuckDB 内存/临时空间 | 采样独立且限时 200ms，不记录 SQL、凭据或事实内容；OOM 记录失败阶段和清理后采样 |

资源配置独立于代码发布：`ANALYTICS_DB_THREADS` 默认 2（数据库全局，不乘连接数），`ANALYTICS_DB_TEMP_DIR` 默认数据目录下 `.tmp`，`ANALYTICS_DB_TEMP_LIMIT_MB=0` 保留 DuckDB 原磁盘默认上限，`ANALYTICS_CHECKPOINT_CACHE_WINDOW_MB=0` 默认关闭。数据库默认预算仍为 1,024 MiB。

## 生产快照上的 Linux 隔离实验

快照包含 9,374,811 条 facts、1,748,341 条 rollups、804,047 个导入对象。文件为 2,305,044,480 字节（2.147 GiB）。下载及解压前后 SHA256 均校验。测试：Linux arm64、本机 Docker、Go 1.26 bookworm、DuckDB v1.5.5，与生产数据库库版本相同；不等同生产节点的 CPU、磁盘与并发负载。

每个 RSS 对比使用新容器/进程，2 CPU；下表为单次样本，耗时不是 p95。查询 primary/gpt-6-sol、快照末尾 24 小时，返回 22,463 条账单候选，认领汇总 20,493 条。时间为对齐阶段，RSS 包含其后的认领计算；未连接生产 ledger，因此不是完整 HTTP 请求延迟。

| 查询模式 | DuckDB 预算 | 进程峰值 RSS | 对齐时间 | 备注 |
| --- | ---: | ---: | ---: | --- |
| 原始宽表 | 1,024 MiB | 956.6 MiB | 2,161 ms | 基线 |
| 本次优化 | 1,024 MiB | 605.9 MiB | 1,204 ms | RSS 约降低 36.7%，时间约降低 44.3% |
| 本次优化 | 256 MiB | 365.0 MiB | 964 ms | 仅隔离调参；不能据此降低生产限额 |
| 全模型，本次优化 | 256 MiB | 360.7 MiB RSS（VmHWM 454.5 MiB） | 8,209 ms | 窗口返回 103,466 条；文件页触碰使 HWM 高于当前 RSS，低预算对大范围性能有明显代价 |

**结果验证**：不只比较计数或哈希。对单模型 22,463 行、全模型 103,466 行的全部账单投影和 completions，执行双向 `EXCEPT ALL`，差异均为 0。另有生成样本覆盖来源授权、API key、模型、端点、流式、无完成记录、窗口外多次完成及 NULL identity。

新进程对可写快照副本导入 100 条 attempt，再执行统计：256 MiB 与 1,024 MiB 预算均成功、重复导入不改变 revision；但这不覆盖持续写入时 ART 索引逐渐驻留，也不证明长期 256 MiB 安全。

同一快照的 trace-only 导入在 1,024 MiB 预算下成功，进程峰值 RSS 约 321.3 MiB；由于 trace/billing 不参与指标 rollup，导入事务不会再扫描并重建分钟/天统计。该数据是单次离线样本，不代表生产峰值。

快照复制/压缩/读取上传体实验（不连接 S3）：无回收时 cgroup peak 达到测试容器 4 GiB 上限，32 MiB 分段回收时约 2.26 GiB；压缩输出长度相同，已有 round-trip 测试验证完整性。复制时间 7.02 → 4.95 秒、总时间 33.33 → 32.40 秒。源数据库读缓存仍占空间；这不是 10 倍降幅，也不能用受容器上限截断的数值作为真实无约束峰值。

cgroup 文件页缓存可能跨容器共享并由首次触页者记账，增量导入实验中的复制还会污染高水位。此处不拿这些 cgroup 数字与生产历史 8.22 GiB 直接做收益百分比。只有同一完整工作负载、明确缓存状态下的持续采样才可评估整体目标。

## 验证与复现

常规验证从 `analytics-api/` 执行：

```sh
TEST_CONTROL_DATABASE_URL='postgres://postgres@127.0.0.1:55479/analytics_test?sslmode=disable' go test ./... -count=1
go vet ./...
go build ./...
go test -race ./... -run 'Test(SpendGate|DatabaseResource|BoundedBilling|BillingClaims|StreamingFact|TraceAndBilling)' -count=1
```

使用专用测试数据库；测试会创建/删除测试状态，不能填写生产 DSN。新增 GitHub CI analytics job 使用隔离 postgres:16 并执行 Go 测试、vet、build。

离线实验入口 `memory_snapshot_test.go` 默认跳过，只有明确设置 `ANALYTICS_MEMORY_SNAPSHOT` 才执行。必须指向不可变离线快照，绝不指向活动 writer。`TestOfflineSpendMemory` 以 read_only 打开；`TestOfflineIncrementalImportMemory` 自动先复制到临时目录。

```sh
ANALYTICS_MEMORY_SNAPSHOT=/offline/history.duckdb \
ANALYTICS_MEMORY_TEST_MODE=compare ANALYTICS_MEMORY_TEST_MB=1024 \
go test -run '^TestOfflineSpendMemory$' -v
```

模式 baseline/optimized 应各自新进程测量；compare 同时运行两种查询只用于结果一致性，不能用于性能对比。全模型设置 `ANALYTICS_MEMORY_ALL_MODELS=1`。快照缓存测试为 `TestOfflineCheckpointMemory`，设置 `ANALYTICS_CHECKPOINT_CACHE_WINDOW_MB=0` 或 `32`。

## 发布边界与后续门槛

本次不降低线上内存预算，不更改线上配置或重启业务。候选代码保持数据库 schema 与快照格式兼容，可回到旧版本。发布前需以隔离候选长期回放覆盖导入 + 查询 + 快照并发，观测排队和 p95，明确低预算与缓存选项的取舍；保留现有健康实例，失败时继续使用已发布快照和配置。

达到约 0.82 GiB 整体峰值仍需进一步处理历史索引驻留及活动 DB 文件缓存，并验证全量回填、长期导入、2/4 倍历史规模与故障恢复。现阶段不以删索引、丢历史、收紧生产预算等方式制造内存降幅。
