// Native Gemini transport adapter. Challenges and scoring remain upstream code.
// Only visible text from candidate zero with a STOP finish can become a sample.
export async function completeGemini(config, prompt, transport, signal) {
  const base = new URL(config.baseUrl);
  if (!['http:', 'https:'].includes(base.protocol) || base.username || base.password || base.search || base.hash) throw Error('invalid_base_url');
  const url = `${base.href.replace(/\/$/, '')}/v1beta/models/${encodeURIComponent(config.model)}:streamGenerateContent?alt=sse`;
  const body = {
    contents: [{role: 'user', parts: [{text: prompt}]}],
    generationConfig: {maxOutputTokens: 8192, ...(config.effort !== 'default' ? {thinkingConfig: {thinkingLevel: config.effort}} : {})},
  };
  const response = await transport(url, config, body, signal);
  if (!response.ok) {
    await response.body?.cancel();
    throw Object.assign(Error('upstream_http'), {httpStatus: response.status});
  }
  let text = '', stopped = false;
  const consume = data => {
    if (!data || typeof data !== 'object' || Array.isArray(data) || data.error || data.promptFeedback?.blockReason) throw Error('invalid_gemini_output');
    const candidates = data.candidates;
    if (!candidates?.length) return; // Usage-only frames are not output.
    if (!Array.isArray(candidates) || candidates.length !== 1 || (candidates[0].index ?? 0) !== 0) throw Error('ambiguous_gemini_output');
    const candidate = candidates[0];
    if (stopped) throw Error('output_after_stop');
    for (const part of candidate.content?.parts || []) {
      if (part.functionCall || part.inlineData || part.fileData) throw Error('non_text_output');
      if (typeof part.text === 'string' && part.thought !== true) text += part.text;
    }
    if (candidate.finishReason) {
      if (candidate.finishReason !== 'STOP') throw Error('incomplete_gemini_output');
      stopped = true;
    }
  };
  if (!response.headers.get('content-type')?.includes('text/event-stream')) {
    consume(await response.json());
  } else {
    if (!response.body) throw Error('no_stream_body');
    const reader = response.body.getReader(), decoder = new TextDecoder();
    let buffer = '';
    const event = frame => {
      const lines = frame.split(/\r?\n/);
      if (lines.some(line => /^event:\s*error\s*$/.test(line))) throw Error('upstream_stream_error');
      const payload = lines.filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n');
      if (payload) consume(JSON.parse(payload));
    };
    try {
      while (true) {
        signal?.throwIfAborted();
        const {value, done} = await reader.read();
        buffer += decoder.decode(value, {stream: !done});
        let boundary;
        while ((boundary = /\r?\n\r?\n/.exec(buffer))) {
          event(buffer.slice(0, boundary.index));
          buffer = buffer.slice(boundary.index + boundary[0].length);
        }
        if (done) { if (buffer.trim()) event(buffer); break; }
      }
    } finally {
      await reader.cancel().catch(() => {});
      reader.releaseLock();
    }
  }
  if (!stopped || !text.trim()) throw Error('incomplete_gemini_output');
  return {text};
}
