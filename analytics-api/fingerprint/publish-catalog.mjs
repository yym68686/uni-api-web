// Download data only. Execute the checked-in runner, never upstream source.
import { createHash, createPrivateKey, createPublicKey, sign, verify } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync, mkdirSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { gzipSync } from 'node:zlib';
import { decodeDetector } from './upstream/detector-codec.ts';

const here = name => new URL(name, import.meta.url);
const read = name => JSON.parse(readFileSync(here(name)));
const sha = bytes => createHash('sha256').update(bytes).digest('hex');
const id = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/;
const knownFormats = {responses:'responses',openai:'chat',gemini:'gemini'};
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
  if (!r || !Object.hasOwn(knownFormats,r.format) || !['low','default'].includes(r.reasoning_effort) || r.stream !== true) return;
  // New modes, tiers, or provider-specific options require an intentional adapter.
  if (Object.keys(r).some(k=>!['model','format','stream','reasoning_effort','response_models','service_tier'].includes(k)) ||
      (r.service_tier != null && r.service_tier !== 'default')) return;
  if (typeof r.model !== 'string' || ![model.id,`${model.family === 'gpt' ? 'openai' : model.family}/${model.id}`].includes(r.model)) return;
  return {protocol:knownFormats[r.format],effort:r.reasoning_effort};
}

export function deriveCatalog(bank, detector, runs) {
  if (bank.models.length !== detector.model_ids.length || bank.models.some((m,i)=>m.id!==detector.model_ids[i] || m.response_count!==detector.response_counts[i])) throw Error('bank and detector disagree');
  const models = {...baseModels}, protocols = {...baseProtocols}, fixtures = read('reference-fixture.json'), held = [];
  for (const v of Object.values(models)) if (!detector.model_ids.includes(v.class_id)) throw Error(`existing class is absent: ${v.class_id}`);
  for (const model of bank.models) {
    if (!id.test(model.id) || Object.hasOwn(models,model.id) || Object.values(baseModels).some(v=>v.class_id===model.id)) continue;
    const relevant = runs.filter(r=>r.model?.id===model.id && Object.hasOwn(model.sources ?? {},r.source?.channel));
    const profiles = relevant.map(r=>profile(r,model));
    const contract = profiles[0];
    if (!contract || profiles.some(p=>!p || p.protocol!==contract.protocol || p.effort!==contract.effort)) {
      held.push(model.id);
      continue;
    }
    const samples = relevant.flatMap(r=>r.samples ?? []).filter(sample=>sample.completion==='complete' &&
      Number.isSafeInteger(sample.expected_count) && sample.expected_count>=292 && sample.expected_count<=332 &&
      typeof sample.text==='string' && sample.text.length<=65536);
    const usable = [...new Map(samples.map(s=>[s.expected_count,s])).values()].slice(0,3);
    if (usable.length<3) { held.push(model.id); continue; }
    models[model.id] = {class_id:model.id,class_name:model.display_name || model.id,effort:contract.effort};
    protocols[model.id] = contract.protocol;
    fixtures[model.id] = usable.map(({text,expected_count})=>({text,expected_count}));
  }
  return {models,protocols,fixtures,held};
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

async function publish(commit, output, keyPath, previousPath) {
  if (!/^[0-9a-f]{40}$/.test(commit) || !output || !keyPath) throw Error('usage: node publish-catalog.mjs <commit> <output> <key.pem> [previous.json]');
  const previous = previousBundle(previousPath);
  if (previous && JSON.parse(Buffer.from(previous.files['manifest.json'],'base64')).commit===commit) {
    console.log('Reference commit already published');
    return;
  }
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
  const {models,protocols,fixtures,held} = deriveCatalog(bank,detector,runs);
  if (previous) {
    const priorModels = JSON.parse(Buffer.from(previous.files['models.json'],'base64'));
    const priorProtocols = JSON.parse(Buffer.from(previous.files['protocols.json'],'base64'));
    for (const [model,ref] of Object.entries(priorModels)) {
      if (models[model]?.class_id!==ref.class_id || models[model]?.effort!==ref.effort || protocols[model]!==priorProtocols[model]) throw Error(`published model contract changed: ${model}`);
    }
  }
  console.log('Awaiting compatible sampling metadata:',held.join(', ') || 'none');
  const files = {
    'models.json':Buffer.from(JSON.stringify(models)),
    'protocols.json':Buffer.from(JSON.stringify(protocols)),
    'manifest.json':Buffer.from(JSON.stringify({repository:'https://github.com/Ikaleio/lm-detector',commit,files:{
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
  writeBundle(output,keyPath,Object.fromEntries(Object.entries(files).map(([name,data])=>[name,data.toString('base64')])),previous);
  console.log(`Verified and signed ${Object.keys(models).length} model profiles at ${commit}`);
}

if (process.argv[1] && import.meta.url===pathToFileURL(process.argv[1]).href) {
  const [commit,output,key,prev,restore] = process.argv.slice(2);
  if (commit==='--restore') {
    const current = previousBundle(prev), target = previousBundle(restore);
    if (!current || !target || target.schema!==1 || target.sequence>=current.sequence) throw Error('restore requires an older verified generation');
    writeBundle(output,key,target.files,current);
  } else if (commit==='--seed') {
    writeBundle(output,key,Object.fromEntries(['models.json','protocols.json','manifest.json','data/unified_bank.json.gz','data/shared_detector.bin.gz']
      .map(name=>[name,readFileSync(here(name)).toString('base64')])));
  } else await publish(commit,output,key,prev);
}
