export const unsupportedEffortMessage = '上游 Responses 兼容接口不支持指纹所需的 reasoning.effort=low；本轮已停止，未降低采样要求或切换协议。';
const terminalMessages = new Map([
  ['group_deleted', '上游报告采样 Key 所属分组已删除；增加站点余额无效。'],
  ['group_disabled', '上游报告采样 Key 所属分组已停用。'],
  ['group_not_allowed', '上游不再允许采样 Key 的用户使用此分组。'],
  ['insufficient_balance', '上游报告采样 Key 的账户余额不足。'],
  ['model_not_found', '上游报告此分组或接口不支持该模型。'],
  ['route_temporarily_disabled', '网关临时停用了匹配渠道，未派发到上游。'],
  ['endpoint_gone', '采样请求返回 HTTP 410；无法据此确定被撤下的是接口还是分组。'],
  ['reference_effort_unsupported', unsupportedEffortMessage],
]);
export const fingerprintRejectionMessage = code => terminalMessages.get(code);
const responseFailures = new Map([
  ['upstream_stream_error', '上游流式响应报告错误。'],
  ['responses_incomplete', 'Responses 响应未完整结束。'],
  ['incomplete', '上游输出未完整结束。'],
  ['incomplete_gemini_output', 'Gemini 响应未完整结束。'],
  ['bad_stream_json', '流式响应不是有效的 JSON。'],
  ['no_stream_body', '上游未返回流式正文。'],
  ['not_json', '上游未返回有效的 JSON 或 SSE。'],
  ['timeout', '采样请求超时。'],
  ['network', '采样请求未能完成连接或读取。'],
]);
export const fingerprintResponseFailure = code => responseFailures.get(code);

const forbiddenCodes = new Map([
  ['GROUP_DELETED', 'group_deleted'],
  ['GROUP_DISABLED', 'group_disabled'],
  ['GROUP_NOT_ALLOWED', 'group_not_allowed'],
  ['INSUFFICIENT_BALANCE', 'insufficient_balance'],
]);

function rejectsReferenceEffort(data) {
  const error = data?.error;
  return error?.type === 'invalid_request_error' && error.code === 'invalid_request' &&
    error.message === 'reasoning is not supported for Claude/Gemini Responses compatibility';
}

export async function fingerprintResponse(response, config) {
  const status = response.status;
  const effort = status === 400 && config.format === 'responses' && config.effort === 'low' && config.model.startsWith('gemini-');
  if (status !== 403 && status !== 404 && status !== 410 && status !== 503 && !effort) return response;
  if (status === 410) {
    void response.body?.cancel().catch(() => {});
    throw Object.assign(Error('upstream_http'), {httpStatus: status, fingerprintCode: 'endpoint_gone'});
  }
  let data;
  try { data = await response.json(); } catch { /* Malformed/oversized errors stay generic. */ }
  const temporarilyDisabled = status === 503 &&
    config.targeted === true &&
    typeof data?.error?.message === 'string' &&
    Object.keys(data.error).length === 1 &&
    data.error.message === 'All matching channels are temporarily disabled';
  // The gateway's json_error envelope preserves the raw upstream JSON in its
  // sole error.message field. Unwrap exactly one layer, not arbitrary strings
  // or recursively nested provider data.
  if (data?.error && Object.keys(data.error).length === 1 && typeof data.error.message === 'string') {
    try { data = JSON.parse(data.error.message); } catch { /* Keep generic. */ }
  }
  const code = data?.error?.code ?? data?.code;
  const fingerprintCode = temporarilyDisabled ? 'route_temporarily_disabled'
    : status === 403 ? forbiddenCodes.get(code)
    : status === 404 && (code === 'model_not_found' || data?.error?.type === 'model_not_found') ? 'model_not_found'
    : effort && rejectsReferenceEffort(data) ? 'reference_effort_unsupported' : undefined;
  // The error body was consumed for classification. Preserve only the status,
  // as the collector deliberately does not persist arbitrary provider messages.
  throw Object.assign(Error('upstream_http'), {httpStatus: status, fingerprintCode});
}
