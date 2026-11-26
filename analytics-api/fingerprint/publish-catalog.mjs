// Download data only. Execute the checked-in runner, never upstream source.
import { createHash, createPrivateKey, createPublicKey, sign, verify } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync, mkdirSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { gzipSync } from 'node:zlib';
import { decodeDetector } from './upstream/detector-codec.ts';
import { analyzeSharedOutputs } from './upstream/shared-detector.ts';

const here = name => new URL(name, import.meta.url);
const read = name => JSON.parse(readFileSync(here(name)));
const sha = bytes => createHash('sha256').update(bytes).digest('hex');
const id = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/;
const catalogBuilderVersion = 3; // Keep in sync with fingerprintCatalogBuilderVersion in Go.
const knownFormats = {responses:'responses',openai:'chat',chat_completions:'chat',gemini:'gemini',messages:'messages',anthropic:'messages'};
const publisher = {gpt:'openai', gemini:'google', glm:'z-ai', claude:'anthropic', grok:'x-ai', kimi:'moonshotai',
  hunyuan:'tencent', mimo:'xiaomi', muse:'meta', seed:'bytedance-seed'};
// These exact public IDs are pinned by model_catalog.json. This maps the
// scoring class only; the sampler still requests the configured public ID.
const consoleAliases = new Map([
  ['claude-fable-5.2','claude-fable-5-2'], ['claude-haiku-5.5','claude-haiku-5-5'],
  ['claude-opus-5.5','claude-opus-5-5'], ['claude-sonnet-5.5','claude-sonnet-5-5'],
]);
const requestFields = new Set(['model','format','stream','reasoning_effort','response_models','service_tier','max_tokens','temperature']);
const baseModels = read('models.json'), baseProtocols = read('protocols.json');

async function upstream(commit, path, limit) {
  const response = await fetch(`https://raw.githubusercontent.com/Ikaleio/lm-detector/${commit}/${path}`, {redirect:'error', signal:AbortSignal.timeout(45_000)});
  if (!response.ok || !response.body) throw Error(`${path}: HTTP ${response.status}`);
  const chunks = [];
  let n = 0;
  for await (const chunk of response.body) {
    n += chunk.length;
    if (n > limit) throw Error(`${path}: exceeds size limit`);
    chunks.push(chunk);
  }
  return Buffer.concat(chunks,n);
}

function profile(run, model) {
  const r = run.request;
  if (!r || !Object.hasOwn(knownFormats,r.format) || r.stream !== true || typeof r.model !== 'string') return;
  const protocol = knownFormats[r.format];
  // The upstream serializer omits the effort field for both "" and "default".
  // An unknown (null/missing) effort is not evidence of the default contract.
  const effort = r.reasoning_effort === '' ? 'default' : r.reasoning_effort;
  if (!['low','default'].includes(effort) &&
      !(effort === 'none' && (protocol === 'chat' || protocol === 'messages')) &&
      !(effort === 'minimal' && protocol === 'chat')) return;
  if (Object.keys(r).some(k=>!requestFields.has(k)) || r.temperature != null ||
      (r.service_tier != null && r.service_tier !== 'default')) return;
  const max = r.max_tokens ?? 8192;
  if (!Number.isSafeInteger(max) || max < 1024 || max > 32768) return;
  const canonical = [model.id,`${model.family === 'gpt' ? 'openai' : model.family}/${model.id}`];
  const vendor = Object.hasOwn(publisher,model.family) ? publisher[model.family] : '';
  const upstreamId = model.id === 'seed-2.1-turbo' ? 'seed-2-1-turbo' : model.id;
  // New namespaces must declare the exact request model as a response model.
  // Never strip an arbitrary namespace or infer an alias from a prefix alone.
  const vendorAlias = vendor && r.model === `${vendor}/${upstreamId}` &&
    Array.isArray(r.response_models) && r.response_models.includes(r.model);
  if (!canonical.includes(r.model) && !vendorAlias) return;
  return {protocol,effort,...(max === 8192 ? {} : {max_tokens:max})};
}

function sampleFixtures(runs, completion) {
  const samples = runs.flatMap(r=>r.samples ?? []).filter(s=>s.completion===completion &&
    Number.isSafeInteger(s.expected_count) && s.expected_count>=292 && s.expected_count<=332 &&
    typeof s.text==='string' && s.text.length<=65536);
  return [...new Map(samples.map(s=>[s.expected_count,s])).values()].slice(0,3);
}

function declaredAlias(model, runs) {
  if (model.family !== 'claude' || !model.id.includes('.')) return '';
  if (consoleAliases.has(model.id)) return consoleAliases.get(model.id);
  const alias = model.id.replaceAll('.', '-');
  return runs.some(r=>Array.isArray(r.request.response_models) && r.request.response_models.includes(alias)) ? alias : '';
}

export function deriveCatalog(bank, detector, runs, validate, previous) {
  if (bank.models.length !== detector.model_ids.length || bank.models.some((m,i)=>m.id!==detector.model_ids[i] || m.response_count!==detector.response_counts[i])) throw Error('bank and detector disagree');
  const models = {...baseModels}, protocols = {...baseProtocols}, fixtures = read('reference-fixture.json'), held = [], deferred = {};
  const classes = new Set(detector.model_ids);
  const defer = (model, reason) => { held.push(model.id); deferred[model.id] = reason; };
  for (const v of Object.values(models)) if (!detector.model_ids.includes(v.class_id)) throw Error(`existing class is absent: ${v.class_id}`);
  for (const model of bank.models) {
    if (!id.test(model.id) || Object.hasOwn(models,model.id) || Object.values(baseModels).some(v=>v.class_id===model.id)) continue;
    const relevant = runs.filter(r=>r.model?.id===model.id && Object.hasOwn(model.sources ?? {},r.source?.channel));
    const groups = new Map();
    for (const run of relevant) {
      const contract = profile(run,model);
      if (!contract) continue;
      const key = JSON.stringify(contract);
      const group = groups.get(key) ?? {contract,runs:[]};
      group.runs.push(run);
      groups.set(key,group);
    }
    if (!groups.size) { defer(model,'request_contract'); continue; }
    const candidates = [...groups.values()].map(group => {
      const complete = sampleFixtures(group.runs,'complete');
      // Historical "unknown" is offline scoring evidence only. Never accept
      // it as a live sample, or use truncated/rejected/undeclared records.
      return {...group,modern:complete.length===3,
        examples:complete.length===3 ? complete : sampleFixtures(group.runs,'unknown')};
    }).filter(g=>g.examples.length===3);
    if (!candidates.length) { defer(model,'reference_samples'); continue; }
    const existing = previous?.models[model.id];
    const matchesExisting = ({contract}) => !!existing && existing.class_id === model.id &&
      existing.effort === contract.effort && existing.max_tokens === contract.max_tokens &&
      previous.protocols[model.id] === contract.protocol;
    // Prefer explicit complete evidence; prefer the old builder's exact
    // default token contract when both variants are present. Do not mix them.
    candidates.sort((a,b)=>Number(matchesExisting(b))-Number(matchesExisting(a)) || Number(b.modern)-Number(a.modern) ||
      Number(!!a.contract.max_tokens)-Number(!!b.contract.max_tokens));
    const pick = candidates[0];
    const usable = pick.examples.map(({text,expected_count})=>({text,expected_count}));
    if (validate && !validate(model.id,usable)) { defer(model,'offline_score'); continue; }
    const {protocol,...request} = pick.contract;
    const entry = {class_id:model.id,class_name:model.display_name || model.id,...request};
    models[model.id] = entry;
    protocols[model.id] = protocol;
    fixtures[model.id] = usable;
    const alias = declaredAlias(model,pick.runs);
    if (alias && id.test(alias) && !classes.has(alias) && !Object.hasOwn(models,alias)) {
      models[alias] = entry;
      protocols[alias] = protocol;
    }
  }
  return {models,protocols,fixtures,held,deferred};
}

function previousBundle(path) {
  if (!path) return;
  const prior = readFileSync(path,'utf8');
  const matched = prior.match(/^\{"signature":"([^"]+)","payload":(\{.*\})\}$/s);
  const key = createPublicKey({key:Buffer.from(readFileSync(here('publisher.pub'),'utf8').trim(),'base64'),type:'spki',format:'der'});
  if (!matched || !verify(null,Buffer.from(matched[2]),key,Buffer.from(matched[1],'base64'))) throw Error('cannot verify last published bundle');
  return JSON.parse(matched[2]);
}

function writeBundle(output,keyPath,files,previous) {
  const payload = JSON.stringify({schema:1,sequence:Math.max(Math.floor(Date.now()/1000),(previous?.sequence ?? 0)+1),files});
  const key = createPrivateKey(readFileSync(keyPath));
  const expected = readFileSync(here('publisher.pub'),'utf8').trim();
  if (createPublicKey(key).export({format:'der',type:'spki'}).toString('base64')!==expected) throw Error('publisher key does not match runtime trust root');
  const raw = `{"signature":"${sign(null,Buffer.from(payload),key).toString('base64')}","payload":${payload}}`;
  if (Buffer.byteLength(raw)>(16<<20)) throw Error('fingerprint bundle exceeds runtime bound');
  writeFileSync(output,raw,{flag:'wx'});
}

async function buildCatalog(commit, previous) {
  if (!/^[0-9a-f]{40}$/.test(commit)) throw Error('expected an immutable upstream commit SHA');
  const sourceFiles = read('scorer-sources.json');
  await Promise.all(Object.entries(sourceFiles).map(async ([path,expected])=>{
    if (sha(await upstream(commit,path,1<<20))!==expected) throw Error(`Scorer or request contract changed: ${path}; review before publishing`);
  }));
  const [bankBytes, detectorBytes, refBytes] = await Promise.all([
    upstream(commit,'data/unified_bank.json',32<<20),upstream(commit,'data/shared_detector.bin',32<<20),upstream(commit,'data/unified_reference.jsonl',64<<20),
  ]);
  const bank = JSON.parse(bankBytes), detector = decodeDetector(detectorBytes);
  if (bank.reference_sha256!==sha(refBytes)) throw Error('reference provenance digest mismatch');
  const runs = refBytes.toString().trimEnd().split('\n').map(line=>JSON.parse(line));
  const existing = previous?.files ? {
    models:JSON.parse(Buffer.from(previous.files['models.json'],'base64')),
    protocols:JSON.parse(Buffer.from(previous.files['protocols.json'],'base64')),
  } : previous;
  const {models,protocols,fixtures,held,deferred} = deriveCatalog(bank,detector,runs,(classId,examples)=>{
    const scored = analyzeSharedOutputs(examples,bank,detector,{allowPartial:true});
    const decision = scored.decision ?? (scored.results.length ? 'scored' : 'unscorable');
    return scored.used_outputs===3 && decision==='scored' && scored.prediction===classId;
  },existing);
  if (existing) {
    for (const [model,ref] of Object.entries(existing.models)) {
      if (models[model]?.class_id!==ref.class_id || models[model]?.effort!==ref.effort ||
          models[model]?.max_tokens!==ref.max_tokens || protocols[model]!==existing.protocols[model]) throw Error(`published model contract changed: ${model}`);
    }
  }
  console.log('Deferred fingerprint reference classes:',held.length ? JSON.stringify(deferred) : 'none');
  const files = {
    'models.json':Buffer.from(JSON.stringify(models)),
    'protocols.json':Buffer.from(JSON.stringify(protocols)),
    'manifest.json':Buffer.from(JSON.stringify({repository:'https://github.com/Ikaleio/lm-detector',commit,catalog_builder_version:catalogBuilderVersion,deferred,files:{
      ...sourceFiles,'data/unified_bank.json':sha(bankBytes),'data/shared_detector.bin':sha(detectorBytes),
    }})),
    'data/unified_bank.json.gz':gzipSync(bankBytes),
    'data/shared_detector.bin.gz':gzipSync(detectorBytes),
  };
  const stage = mkdtempSync(join(tmpdir(),'fingerprint-offline-'));
  try {
    mkdirSync(join(stage,'data'));
    for (const [name,data] of Object.entries(files)) writeFileSync(join(stage,name),data);
    writeFileSync(join(stage,'reference-fixture.json'),JSON.stringify(fixtures));
    execFileSync(process.execPath,['--max-old-space-size=192','--max-semi-space-size=4','--test',here('catalog-offline.test.mjs').pathname],{
      env:{PATH:process.env.PATH,FINGERPRINT_DATA_DIR:stage},stdio:'inherit',
    });
  } finally { rmSync(stage,{recursive:true,force:true}); }
  return Object.fromEntries(Object.entries(files).map(([name,data])=>[name,data.toString('base64')]));
}

async function publish(commit, output, keyPath, previousPath) {
  if (!output || !keyPath) throw Error('usage: node publish-catalog.mjs <commit> <output> <key.pem> [previous.json]');
  const previous = previousBundle(previousPath);
  const manifest = previous && JSON.parse(Buffer.from(previous.files['manifest.json'],'base64'));
  if (manifest?.commit===commit && manifest.catalog_builder_version>=catalogBuilderVersion) {
    console.log('Reference commit already published');
    return;
  }
  writeBundle(output,keyPath,await buildCatalog(commit,previous),previous);
}

async function derive(commit, output, prior) {
  if (!output) throw Error('usage: node publish-catalog.mjs --derive <commit> <output> [current-contracts.json]');
  // This file is private IPC with the Go updater, not an externally accepted bundle.
  const payload = JSON.stringify({schema:1,files:await buildCatalog(commit,prior ? JSON.parse(readFileSync(prior)) : undefined)});
  if (Buffer.byteLength(payload)>(16<<20)) throw Error('fingerprint bundle exceeds runtime bound');
  writeFileSync(output,payload,{flag:'wx',mode:0o600});
}

if (process.argv[1] && import.meta.url===pathToFileURL(process.argv[1]).href) {
  const [commit,output,key,prev,restore] = process.argv.slice(2);
  if (commit==='--derive') {
    await derive(output,key,prev);
  } else if (commit==='--restore') {
    const current = previousBundle(prev), target = previousBundle(restore);
    if (!current || !target || target.schema!==1 || target.sequence>=current.sequence) throw Error('restore requires an older verified generation');
    writeBundle(output,key,target.files,current);
  } else if (commit==='--seed') {
    writeBundle(output,key,Object.fromEntries(['models.json','protocols.json','manifest.json','data/unified_bank.json.gz','data/shared_detector.bin.gz']
      .map(name=>[name,readFileSync(here(name)).toString('base64')])));
  } else await publish(commit,output,key,prev);
}
