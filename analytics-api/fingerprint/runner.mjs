// Go supplies only ephemeral loopback bridge tokens to the shared worker.
// No credentials or upstream error bodies are returned; Go owns job leases.
import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { createHash, randomUUID } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import { createServer } from 'node:http';
import { generateChallenges } from './upstream/challenge-browser.js';
import { complete } from './upstream/detection.ts';
import { analyzeSharedOutputs, supportsSharedDetector } from './upstream/shared-detector.ts';
import { decodeDetector } from './upstream/detector-codec.ts';
import { COMPLETION_TIMEOUT_MS } from './upstream/completion-request.ts';
import { completeGemini } from './gemini.mjs';

const modelClasses = JSON.parse(readFileSync(new URL('./models.json', import.meta.url)));
export const models = Object.keys(modelClasses);
const manifest = JSON.parse(readFileSync(new URL('./manifest.json', import.meta.url)));
export function loadReference() {
  const load = name => {
    const raw = gunzipSync(readFileSync(new URL(`./data/${name}.gz`, import.meta.url)));
    if (createHash('sha256').update(raw).digest('hex') !== manifest.files[`data/${name}`]) throw Error('reference_integrity');
    return name.endsWith('.bin') ? decodeDetector(raw) : JSON.parse(raw);
  };
  const bank = load('unified_bank.json'), detector = load('shared_detector.bin');
  if (!supportsSharedDetector(bank, detector)) throw Error('reference_incompatible');
  for (const entry of Object.values(modelClasses)) {
    if (!detector.model_ids.includes(entry.class_id)) throw Error('missing_reference_class');
  }
  return { bank, detector };
}
export function score(model, outputs, reference = loadReference()) {
  if (!models.includes(model)) throw Error('unsupported_model');
  const analysis = analyzeSharedOutputs(outputs, reference.bank, reference.detector);
  // The probability is for the requested class, even when another ranks first.
  const target = modelClasses[model];
  const expected = analysis.results.find(r => r.model === target.class_id);
  return {
    model, status: analysis.results.length ? (analysis.prediction === target.class_id ? 'match' : 'mismatch') : 'insufficient',
    probability: expected?.probability ?? null,
    prediction: analysis.prediction, prediction_name: analysis.prediction_name,
    expected_class: target.class_name, candidates: analysis.results.slice(0, 5),
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
export async function collect(config, transport, signal) {
  if (!models.includes(config.model)) throw Error('unsupported_model');
  const challenges = generateChallenges(3), outputs = [], samples = [];
  const started = Date.now();
  // Stored gateway URLs may use private HTTP; retain upstream URL validation
  // using HTTPS, then restore the trusted configuration's transport protocol.
  const base = new URL(config.base_url);
  if (!['http:', 'https:'].includes(base.protocol)) throw Error('invalid_base_url');
  const protocol = base.protocol; base.protocol = 'https:';
  for (const challenge of challenges) {
    signal?.throwIfAborted();
    const requestId = 'fingerprint-' + randomUUID(), at = Date.now();
    try {
      const format = config.format || 'responses';
      if (!['responses', 'anthropic', 'openai', 'gemini'].includes(format)) throw Error('unsupported_protocol');
      const cfg = {baseUrl: base.href, apiKey: config.key, model: config.model, format, effort: modelClasses[config.model].effort, stream: true};
      const send = transport || (async (url, cfg, body, requestSignal) => {
        const destination = new URL(url); destination.protocol = protocol;
        const response = await fetch(destination, { method: 'POST', redirect: 'error', signal: signal ? AbortSignal.any([signal, requestSignal]) : requestSignal,
          headers: { ...(cfg.format === 'gemini' ? {'x-goog-api-key':cfg.apiKey} : cfg.format === 'anthropic' ? {'x-api-key':cfg.apiKey,'anthropic-version':'2023-06-01'} : {Authorization: `Bearer ${cfg.apiKey}`}), 'Content-Type': 'application/json', Accept: 'text/event-stream',
            'X-Request-ID': requestId, ...(config.provider ? { 'X-Uni-API-Provider': config.provider,
              ...(config.allow_unconfigured ? { 'X-Uni-API-Probe-Unconfigured-Model': 'true' } : {}) } : {}) },
          body: JSON.stringify(body) });
        return response;
      });
      const boundedSend = async (...args) => bounded(await send(...args));
      const timeout = AbortSignal.timeout(COMPLETION_TIMEOUT_MS);
      const requestSignal = signal ? AbortSignal.any([signal, timeout]) : timeout;
      const result = format === 'gemini'
        ? await completeGemini(cfg, challenge.prompt, boundedSend, requestSignal)
        : await complete(cfg, challenge.prompt, '', signal, undefined, boundedSend);
      outputs.push({ text: result.text, expected_count: challenge.expected_count });
      samples.push({ request_id: requestId, state: 'done', duration_ms: Date.now() - at, expected_count: challenge.expected_count });
    } catch (error) {
      signal?.throwIfAborted();
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
  // Standalone detection validates before paid I/O. The shared server validates
  // once before listening and reuses its immutable reference for every job.
  const reference = loadReference();
  return scoreCollected(await collect(config, transport), reference);
}

// One immutable bank and one Node runtime serve concurrent collectors. Scoring
// runs synchronously on this event loop, so it never creates extra scoring heaps.
export async function serve() {
  const reference = loadReference(), token = randomUUID(), active = new Set();
  const server = createServer(async (req, res) => {
    if (req.method !== 'POST' || req.url !== '/detect' || req.headers.authorization !== `Bearer ${token}`) {
      res.writeHead(403).end(); return;
    }
    if (active.size >= 50) { res.writeHead(503).end(); return; }
    const controller = new AbortController();
    active.add(controller);
    req.on('aborted', () => controller.abort());
    res.on('close', () => { if (!res.writableFinished) controller.abort(); });
    try {
      let input = '';
      for await (const chunk of req) {
        input += chunk;
        if (input.length > 65536) throw Error('invalid_input');
      }
      const config = JSON.parse(input), base = new URL(config.base_url);
      // Only Go's authenticated ephemeral bridge is an allowed destination.
      if (base.protocol !== 'http:' || base.hostname !== '127.0.0.1' || base.username || base.password) throw Error('invalid_bridge');
      const collected = await collect(config, undefined, controller.signal);
      controller.signal.throwIfAborted();
      const result = scoreCollected(collected, reference);
      res.writeHead(200, {'Content-Type':'application/json'}).end(JSON.stringify(result));
    } catch {
      if (!res.destroyed) res.writeHead(500).end('fingerprint_worker_failed');
    } finally { active.delete(controller); }
  });
  server.requestTimeout = 800_000;
  server.headersTimeout = 5_000;
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  process.stdout.write(JSON.stringify({port: server.address().port, token}) + '\n');
  // Losing the owning Go process stops every probe; no job is replayed.
  process.stdin.resume();
  process.stdin.on('end', () => {
    for (const controller of active) controller.abort();
    server.close(); server.closeAllConnections();
  });
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.argv.includes('--serve')) {
      await serve();
    } else {
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
    }
  } catch {
    process.stderr.write('fingerprint_worker_failed\n'); process.exitCode = 1;
  }
}
