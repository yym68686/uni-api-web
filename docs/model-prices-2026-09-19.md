# 检测模型参考价格（2026-09-19）

单位：美元 / 百万 token。价格设置与检测设置共用 `analytics-api/model_catalog.json`，共 22 项。以下为标准文本请求的基准价格；来源和适用条件随每个模型记录。

| 模型 | 输入 | 输出 | 缓存读取 | 官方来源 |
| --- | ---: | ---: | ---: | --- |
| gpt-6-astra | 10 | 50 | 1 | [官方文档](https://developers.openai.com/api/docs/pricing) |
| gpt-5.6-sol | 4 | 20 | 0.4 | [官方文档](https://developers.openai.com/api/docs/pricing) |
| gpt-5.6-terra | 2 | 12 | 0.2 | [官方文档](https://developers.openai.com/api/docs/pricing) |
| gpt-5.6-luna | 0.2 | 1.2 | 0.02 | [官方文档](https://developers.openai.com/api/docs/pricing) |
| gpt-5.5 | 5 | 30 | 0.5 | [官方文档](https://developers.openai.com/api/docs/pricing) |
| codex-auto-review | 未公布 | 未公布 | 未公布 | [官方文档](https://developers.openai.com/api/docs/pricing) |
| glm-5.3 | 1.4 | 4.4 | 0.26 | [官方文档](https://docs.z.ai/guides/overview/pricing) |
| glm-5.3-flash | 0.15 | 0.5 | 0.03 | [官方文档](https://docs.z.ai/guides/overview/pricing) |
| kimi-k3 | 3 | 15 | 0.3 | [官方文档](https://forum.moonshot.ai/t/kimi-k3-is-here-our-most-capable-model/480) |
| deepseek-4.1-flash | 0.3 | 1.2 | 0.006 | [官方文档](https://api-docs.deepseek.com/quick_start/pricing/) |
| deepseek-4-pro | 1.32 | 3.96 | 0.044 | [官方文档](https://api-docs.deepseek.com/quick_start/pricing/) |
| grok-4.6 | 2 | 6 | 0.5 | [官方文档](https://docs.x.ai/developers/pricing) |
| gemini-3.1-pro | 2 | 12 | 0.2 | [官方文档](https://ai.google.dev/gemini-api/docs/pricing) |
| gemini-3.8-flash | 0.75 | 3.75 | 0.075 | [官方文档](https://ai.google.dev/gemini-api/docs/pricing) |
| claude-fable-5 | 10 | 50 | 1 | [官方文档](https://platform.claude.com/docs/en/about-claude/pricing) |
| claude-fable-5-1 | 10 | 50 | 0.25 | [官方文档](https://platform.claude.com/docs/en/about-claude/pricing) |
| claude-opus-5 | 5 | 25 | 0.5 | [官方文档](https://platform.claude.com/docs/en/about-claude/pricing) |
| claude-sonnet-5 | 2 | 10 | 0.2 | [官方文档](https://platform.claude.com/docs/en/about-claude/pricing) |
| claude-opus-4-8 | 5 | 25 | 0.5 | [官方文档](https://platform.claude.com/docs/en/about-claude/pricing) |
| claude-opus-4-6 | 5 | 25 | 0.5 | [官方文档](https://platform.claude.com/docs/en/about-claude/pricing) |
| claude-sonnet-4-6 | 3 | 15 | 0.3 | [官方文档](https://platform.claude.com/docs/en/about-claude/pricing) |
| claude-haiku-4-5-20251001 | 1 | 5 | 0.1 | [官方文档](https://platform.claude.com/docs/en/about-claude/pricing) |

## 口径与继承规则

- OpenAI 使用 Standard 短上下文；GPT-5.6 Sol、Gemini 3.8 Flash 使用核对当日生效的优惠价。优惠期截止及长上下文差异记录在各模型说明，当前估算不自动按请求长度或日期切换价档。
- DeepSeek 使用当前官方高峰价。官方更新说明明确 V4 Pro 在 9 月 14 日后继续服务、计费不变；未采用已被更新说明更正的降价转路由公告。非高峰价为表中一半。
- Gemini 3.1 Pro 采用官方 `gemini-3.1-pro-preview` 价目；DeepSeek 检测名称分别对应官方 `deepseek-flash`（V4.1 Flash）、`deepseek-v4-pro`。
- Claude 记录官方 5 分钟及 1 小时缓存写入单价；OpenAI 新型号记录通用缓存写入单价。未单列缓存写入费的模型，写入 token 按普通输入估算。缓存存储、搜索工具、图片/音频额外计费不在 token 基准估算内。
- `codex-auto-review` 未找到公开官方价格。保留未确认状态，界面显示未公布，不能作为零价格使用。
- 最长完整模型前缀优先，只匹配模型本身或后接 `-` 的派生名称。`gemini-3.1-pro-search` 使用基础价格，`glm-5.3-flash-*` 不会误用 `glm-5.3`。已有后缀独立价格不覆盖基础价格。
- 手动保存的价格仍是持久配置；新默认值只补缺失项或旧的全零、未确认 `fact-discovered` 占位项。S3 价格文件依旧为配置权威来源，不需要重新发布代码即可修改或恢复价格。

## 验证

- Go 回归覆盖旧 S3 配置回放、手动未确认价格保留、后缀缓存费用计算、最长前缀及版本边界。
- 前端回归覆盖与检测清单一致、历史后缀隐藏、后缀单价核验与未定价状态。
- 浏览器检查桌面和手机布局、搜索、22 个条目、未公布价格不显示为免费。
