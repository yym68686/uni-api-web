export const successRateHelp =
  "成功率 = 成功 ÷（成功 + 失败），按渠道尝试计数；重试分别计数，取消和跳过不计入分母。Codex /v1/responses 流式请求须收到上游 response.completed SSE 事件才算成功；HTTP 200 或空白文本不代表成功；response.incomplete（含 max_output_tokens）、断流或 EOF 且未收到完成事件，均算失败。无文本但正常完成的工具调用可算成功。新规则依赖来源采集终止事件；未保留终止证据的旧历史沿用原记录，不能追溯重判。";
