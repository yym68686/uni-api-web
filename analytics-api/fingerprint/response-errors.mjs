// Never expose untrusted upstream text. Recognize only the exact rejection
// observed in production, and only when the request required that parameter.
export const unsupportedEffortMessage = '上游 Responses 兼容接口不支持指纹所需的 reasoning.effort=low；本轮已停止，未降低采样要求或切换协议。';

function rejectsReferenceEffort(data) {
  const error = data?.error;
  return error?.type === 'invalid_request_error' && error.code === 'invalid_request' &&
    error.message === 'reasoning is not supported for Claude/Gemini Responses compatibility';
}

export async function fingerprintResponse(response, config) {
  if (response.status !== 400 || config.format !== 'responses' || config.effort !== 'low' || !config.model.startsWith('gemini-')) return response;
  let data;
  try { data = await response.json(); } catch { /* Malformed/oversized errors stay generic. */ }
  // The gateway's json_error envelope preserves the raw upstream JSON in its
  // sole error.message field. Unwrap exactly one layer, not arbitrary strings
  // or recursively nested provider data.
  if (data?.error && Object.keys(data.error).length === 1 && typeof data.error.message === 'string') {
    try { data = JSON.parse(data.error.message); } catch { /* Keep generic. */ }
  }
  if (rejectsReferenceEffort(data)) {
    throw Object.assign(Error('reference_effort_unsupported'), {httpStatus: 400, fingerprintCode: 'reference_effort_unsupported'});
  }
  // The error body was consumed for classification. Preserve only the status,
  // as the collector deliberately does not persist arbitrary provider messages.
  throw Object.assign(Error('upstream_http'), {httpStatus: 400});
}
