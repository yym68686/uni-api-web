# 余额不足后消费金额持续为下界

调查窗口：2026-09-19 20:56:00–21:56:55（Asia/Shanghai）。只读取运行中网关日志、历史快照和已同步账单；没有重放模型请求或修改业务路由。

两个 xiaobai 渠道各有 39 次未匹配账单的 HTTP 403。保留的网关日志逐请求核对后，各 37 次可证明为余额不足拒绝：一共 68 次鉴权余额不足，6 次等待并发槽位后的余额资格检查失败。sub2api 的相应源码均在模型调用和 RecordUsage 前返回，不生成消费账单。

精确错误正文指纹：

- `7650844e093da022f530f60d448c6e401ca17d5efd97d38978acf34e43cdcb71` 对应 `{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}`。
- `89698b2e03311b2e3ab9b559b2f96f02aaf2caa7bf654e3a786cc360928bd9b7` 对应 `{"error":{"message":"insufficient balance","type":"billing_error"}}`。

依据参考仓库 sub2api 的 `api_key_auth.go`、`openai_gateway_handler.go`（CheckBillingEligibility）、`gateway_handler.go`（billingErrorDetails）和 `billing_cache_service.go`。线上日志指纹与这两个完整响应一致，而不是仅根据状态码或缺少账单推断。

剩余四次 HTTP 403 来自 `/v1/alpha/search`，旧版通用转发日志未保留正文指纹，无法补证。另一次 HTTP 200 实际为流内失败（semantic_status_code=401），无法证明其计费金额，也不得当作成功或免费。

修复：网关在三条已有错误正文读取路径记录 SHA-256；控制台据上述严格证据确认零费用，单独返回 confirmed_unbilled_attempts。真实账单仍优先；未知失败不免单。旧日志补证独立存储、精确匹配，保持原始 S3 事实和上游账单不变。新快照 cache-v4 保持旧快照兼容读取，避免回滚旧代码时读取未知列。

验证包括充值前拒绝和充值后正常账单混合、无响应 ID 的拒绝、重复 ID、错误来源/尝试、正金额账单优先、HTTP 200/未知403不免单、老快照恢复；网关覆盖 Responses 流式/非流式、Messages、Chat、重试及对冲。前端确认已核对次数包含有证据的零费用请求。
