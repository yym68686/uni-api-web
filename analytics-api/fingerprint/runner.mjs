// This process receives credentials over stdin, never argv. No credentials or
// upstream error bodies are returned. The Go worker owns its lifetime/lease.
import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { createHash, randomUUID } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import { generateChallenges } from './upstream/challenge-browser.js';
import { complete } from './upstream/detection.ts';
import { analyzeSharedOutputs, supportsSharedDetector } from './upstream/shared-detector.ts';

export const models = ['gpt-6-astra', 'gpt-6.1-sol'];
const manifest = JSON.parse(readFileSync(new URL('./manifest.json', import.meta.url)));
export function loadReference() {
  const load = name => {
    const raw = gunzipSync(readFileSync(new URL(`./data/${name}.gz`, import.meta.url)));
    if (createHash('sha256').update(raw).digest('hex') !== manifest.files[`data/${name}`]) throw Error('reference_integrity');
    return JSON.parse(raw);
  };
  const bank = load('unified_bank.json'), detector = load('shared_detector.json');
  if (!supportsSharedDetector(bank, detector)) throw Error('reference_incompatible');
  return { bank, detector };
}
export function score(model, outputs, reference = loadReference()) {
  if (!models.includes(model)) throw Error('unsupported_model');
  const analysis = analyzeSharedOutputs(outputs, reference.bank, reference.detector);
  // Both requested names belong to this single reference class. The probability
  // shown is always for the requested class, even when another class ranks first.
  const expected = analysis.results.find(r => r.model === 'gpt-6-astra');
  return {
    model, status: analysis.results.length ? (analysis.prediction === 'gpt-6-astra' ? 'match' : 'mismatch') : 'insufficient',
    probability: expected?.probability ?? null,
    prediction: analysis.prediction, prediction_name: analysis.prediction_name,
    expected_class: 'gpt-6-astra/6.1-sol', candidates: analysis.results.slice(0, 5),
    used_outputs: analysis.used_outputs, diagnostics: analysis.diagnostics,
    probability_scope: analysis.probability_scope ?? null,
    reference_commit: manifest.commit, base_sha256: reference.detector.base_sha256,
  };
}

// Protect the parent from arbitrarily large/error responses, including SSE.
function bounded(response) {
  if (!response.body) return response;
  let bytes = 0;
  return new Response(response.body.pipeThrough(new TransformStream({
    transform(chunk, controller) {
      bytes += chunk.byteLength;
      if (bytes > 2 * 1024 * 1024) throw Error('response_too_large');
      controller.enqueue(chunk);
    },
  })), { status: response.status, headers: response.headers });
}
export async function collect(config, transport) {
  if (!models.includes(config.model)) throw Error('unsupported_model');
  const challenges = generateChallenges(3), outputs = [], samples = [];
  const started = Date.now();
  // Stored gateway URLs may use private HTTP; retain upstream URL validation
  // using HTTPS, then restore the trusted configuration's transport protocol.
  const base = new URL(config.base_url);
  if (!['http:', 'https:'].includes(base.protocol)) throw Error('invalid_base_url');
  const protocol = base.protocol; base.protocol = 'https:';
  for (const challenge of challenges) {
    const requestId = 'fingerprint-' + randomUUID(), at = Date.now();
    try {
      const result = await complete({ baseUrl: base.href, apiKey: config.key, model: config.model,
        format: config.format === 'anthropic' ? 'anthropic' : 'responses', effort: 'low', stream: true }, challenge.prompt, '', undefined, undefined,
      transport || (async (url, cfg, body, signal) => {
        const destination = new URL(url); destination.protocol = protocol;
        const response = await fetch(destination, { method: 'POST', redirect: 'error', signal,
          headers: { ...(cfg.format === 'anthropic' ? {'x-api-key':cfg.apiKey,'anthropic-version':'2023-06-01'} : {Authorization: `Bearer ${cfg.apiKey}`}), 'Content-Type': 'application/json', Accept: 'text/event-stream',
            'X-Request-ID': requestId, ...(config.provider ? { 'X-Uni-API-Provider': config.provider,
              ...(config.allow_unconfigured ? { 'X-Uni-API-Probe-Unconfigured-Model': 'true' } : {}) } : {}) },
          body: JSON.stringify(body) });
        return bounded(response);
      }));
      outputs.push({ text: result.text, expected_count: challenge.expected_count });
      samples.push({ request_id: requestId, state: 'done', duration_ms: Date.now() - at, expected_count: challenge.expected_count });
    } catch (error) {
      outputs.push({ text: '', expected_count: challenge.expected_count });
      samples.push({ request_id: requestId, state: 'error', duration_ms: Date.now() - at,
        expected_count: challenge.expected_count, http_status: Number.isInteger(error.httpStatus) ? error.httpStatus : undefined,
        // Do not persist arbitrary messages from an untrusted endpoint.
        message: '请求失败或输出不完整' });
    }
  }
  return { model: config.model, outputs, samples, checked_at: Math.floor(Date.now() / 1000), duration_ms: Date.now() - started };
}
export function scoreCollected(collected, reference = loadReference()) {
  const started = Date.now();
  const result = score(collected.model, collected.outputs, reference);
  if (collected.samples.some(s => s.state === 'error')) { result.status = 'error'; result.probability = null; }
  return { ...result, samples: collected.samples, checked_at: collected.checked_at, duration_ms: collected.duration_ms + Date.now() - started };
}
export async function detect(config, transport) {
  // Standalone detection validates before paid I/O. In collect/score mode Go
  // validates once per immutable deployment before starting any collectors.
  const reference = loadReference();
  return scoreCollected(await collect(config, transport), reference);
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    let input = '';
    for await (const chunk of process.stdin) { input += chunk; if (input.length > 8 * 1024 * 1024) throw Error('invalid_input'); }
    const config = JSON.parse(input);
    let result;
    switch (config.operation) {
      case 'validate': loadReference(); result = { validated: true }; break;
      case 'collect': result = await collect(config); break;
      case 'score_collected': result = scoreCollected(config); break;
      case 'score': result = score(config.model, config.outputs); break;
      default: result = await detect(config);
    }
    process.stdout.write(JSON.stringify(result));
  } catch {
    process.stderr.write('fingerprint_worker_failed\n'); process.exitCode = 1;
  }
}
