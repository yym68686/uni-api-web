import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { collect, detect, loadReference, models, score, scoreCollected } from './runner.mjs';
import { analyzeSharedOutputs } from './upstream/shared-detector.ts';
import { generateChallenges } from './upstream/challenge-browser.js';
import { completeGemini } from './gemini.mjs';
const fixture = JSON.parse(readFileSync(new URL('./reference-fixture.json', import.meta.url)));
const reference = loadReference();

test('separate collection and scoring preserve results and never return raw outputs', async () => {
  let active=0,maxActive=0,calls=0;
  const collected=await collect({base_url:'https://mock.example',key:'secret',model:'gpt-6-astra'},async()=>{
    maxActive=Math.max(maxActive,++active);
    await new Promise(resolve=>setTimeout(resolve,1));
    active--;calls++;
    return Response.json({status:'completed',output:[{type:'message',role:'assistant',content:[{type:'output_text',text:fixture['gpt-6-astra'][0].text}]}]});
  });
  assert.equal(calls,3);assert.equal(maxActive,1);
  const actual=scoreCollected(collected,reference);
  const expected=score(collected.model,collected.outputs,reference);
  assert.equal(actual.status,expected.status);
  assert.equal(actual.probability,expected.probability);
  assert.deepEqual(actual.candidates,expected.candidates);
  assert.deepEqual(actual.samples,collected.samples);
  assert.equal(actual.outputs,undefined);
  assert.equal(actual._resume,undefined);
  collected.samples[0].state='error';
  assert.equal(scoreCollected(collected,reference).status,'error');
  assert.equal(scoreCollected(collected,reference).probability,null);
});

test('partial rounds retain only valid numeric evidence and request only missing samples',async()=>{
  const config={base_url:'https://mock.example',key:'secret',model:'gpt-6-astra'};
  const checkpoints=[];
  let calls=0;
  const response=text=>Response.json({status:'completed',output:[{type:'message',role:'assistant',content:[{type:'output_text',text}]}]});
  const first=await collect(config,async()=>++calls===1?response(fixture['gpt-6-astra'][0].text):Response.json({error:{}},{status:429}),undefined,async c=>checkpoints.push(scoreCollected(c,reference,true)));
  const a=scoreCollected(first,reference,true);
  assert.equal(calls,3);assert.equal(a.used_outputs,1);assert.equal(a._resume.entries.length,1);assert.equal(checkpoints[0].used_outputs,1);
  assert.equal(a._resume.entries[0].text,undefined);assert.ok(a._resume.entries[0].numbers.every(Number.isInteger));
  calls=0;
  const second=await collect({...config,resume:a._resume},async()=>++calls===1?response(fixture['gpt-6-astra'][1].text):Response.json({error:{}},{status:429}));
  const b=scoreCollected(second,reference,true);
  assert.equal(calls,2);assert.equal(b.used_outputs,2);assert.equal(b._resume.entries[0].sample.request_id,a._resume.entries[0].sample.request_id);
  calls=0;
  const third=await collect({...config,resume:b._resume},async()=>{calls++;return response(fixture['gpt-6-astra'][2].text)});
  const c=scoreCollected(third,reference,true);
  assert.equal(calls,1);assert.equal(c.used_outputs,3);assert.equal(c.status,'match');assert.equal(new Set(c._resume.entries.map(e=>e.expected_count)).size,3);
  assert.deepEqual(c.candidates,score('gpt-6-astra',fixture['gpt-6-astra'],reference).candidates);
  calls=0;
  await collect({...config,resume:c._resume},async()=>{calls++;return response(fixture['gpt-6-astra'][0].text)});
  assert.equal(calls,3,'a completed round must start over on the next explicit detection');
});

test('resume rejects incompatible or invalid evidence and checkpoint failure never repeats a paid call',async()=>{
  const config={base_url:'https://mock.example',key:'secret',model:'gpt-6-astra'};
  const response=()=>Response.json({status:'completed',output:[{type:'message',role:'assistant',content:[{type:'output_text',text:fixture['gpt-6-astra'][0].text}]}]});
  const initial=scoreCollected(await collect(config,async()=>response()),reference,true)._resume;
  initial.entries=initial.entries.slice(0,1);
  for(const mutation of [r=>r.model='gpt-6.1-sol',r=>r.format='gemini',r=>r.reference_key='old',r=>r.entries[0].numbers=[1,2,3],r=>r.entries[0].numbers[0]=356,r=>r.entries.push(r.entries[0])]){
    const resume=structuredClone(initial);mutation(resume);let calls=0;
    await collect({...config,resume},async()=>{calls++;return response()});assert.equal(calls,3);
  }
  let calls=0;
  await assert.rejects(collect(config,async()=>{calls++;return response()},undefined,async()=>{throw Error('persistence failed')}),/persistence failed/);
  assert.equal(calls,1);
  const failed=await collect({...config,resume:initial},async()=>Response.json({error:{}},{status:429}));
  assert.deepEqual(scoreCollected(failed,reference,true)._resume.entries,initial.entries);
});

test('full reference bank, original scoring and the merged Astra/Sol class are preserved', () => {
  assert.equal(reference.bank.models.length, 59);
  for (const model of ['gpt-6-astra', 'gpt-6.1-sol']) {
    const result = score(model, fixture['gpt-6-astra'], reference);
    const original = analyzeSharedOutputs(fixture['gpt-6-astra'], reference.bank, reference.detector);
    assert.equal(result.status, 'match');
    assert.equal(result.probability, original.results.find(r => r.model === 'gpt-6-astra').probability);
    assert.deepEqual(result.candidates, original.results.slice(0,5));
    assert.equal(result.probability_scope, 'reference-closed-set');
  }
});
test('mismatch probability is for requested class rather than confidence in the wrong model', () => {
  const result = score('gpt-6.1-sol',fixture['claude-haiku-4.5'],reference);
  assert.equal(result.status,'mismatch');
  assert.equal(result.prediction,'claude-haiku-4.5');
  assert.ok(result.probability < result.candidates[0].probability);
});
test('invalid/partial samples never receive a fabricated match probability', () => {
  for (const outputs of [[],fixture['gpt-6-astra'].slice(0,2),Array(3).fill({text:'1,2,3',expected_count:300})]) {
    const result=score('gpt-6-astra',outputs,reference);
    assert.equal(result.status,'insufficient'); assert.equal(result.probability,null);
  }
  assert.throws(()=>score('gpt-5.5',fixture['gpt-6-astra'],reference),/unsupported_model/);
});
test('generates three independent supported-length challenges', () => {
  const challenges=generateChallenges(3);
  assert.equal(new Set(challenges.map(c=>c.expected_count)).size,3);
  assert.equal(new Set(challenges.map(c=>c.id)).size,3);
  assert.ok(challenges.every(c=>c.expected_count>=292 && c.expected_count<=332));
});
test('uses targeted Responses requests, strict completion, and no retries or secret persistence', async () => {
  let calls=0;const requests=[];
  const transport=async(_url,config,body)=>{
    calls++;requests.push({config,body});
    if(calls===2)return Response.json({error:{message:'secret-key should not persist'}},{status:429});
    return Response.json({status:'completed',output:[{type:'message',role:'assistant',content:[{type:'output_text',text:fixture['gpt-6-astra'][0].text}]}]});
  };
  const result=await detect({base_url:'https://mock.example',key:'secret-key',model:'gpt-6.1-sol'},transport);
  assert.equal(calls,3);assert.equal(result.status,'error');assert.equal(result.probability,null);
  for(const {config,body} of requests){
    assert.equal(config.model,'gpt-6.1-sol');assert.deepEqual(body.reasoning,{effort:'low'});
    assert.equal(body.stream,true);assert.equal(body.store,false);assert.equal(body.max_output_tokens,8192);
    assert.ok(body.input[0].content.includes('355'));
  }
  assert.equal(result.samples[1].http_status,429);assert.ok(!JSON.stringify(result).includes('secret-key'));
});
test('configured private HTTP endpoints keep provider targeting and never follow redirects', async () => {
  const original=globalThis.fetch;let calls=0;
  globalThis.fetch=async(url,init)=>{
    calls++;assert.equal(url.href,'http://gateway.internal:8000/v1/responses');
    assert.equal(init.headers['X-Uni-API-Provider'],'test-provider');
    assert.equal(init.headers['X-Uni-API-Probe-Unconfigured-Model'],'true');
    assert.equal(init.headers.Authorization,'Bearer secret');assert.equal(init.redirect,'error');
    return Response.json({status:'completed',output:[{type:'message',role:'assistant',content:[{type:'output_text',text:fixture['gpt-6-astra'][0].text}]}]});
  };
  try {
    const result=await detect({base_url:'http://gateway.internal:8000',key:'secret',provider:'test-provider',allow_unconfigured:true,model:'gpt-6-astra'});
    assert.equal(calls,3);assert.equal(result.used_outputs,3);
  } finally {globalThis.fetch=original}
});
test('error SSE and truncated completion cannot count as complete samples', async () => {
  for(const sse of ['data: {"type":"response.output_text.delta","delta":"1 2 3"}\n\n','data: {"type":"error","message":"private"}\n\n']) {
    const result=await detect({base_url:'https://mock.example',key:'secret',model:'gpt-6-astra'},async()=>new Response(sse,{headers:{'content-type':'text/event-stream'}}));
    assert.equal(result.status,'error');assert.equal(result.probability,null);
  }
});

test('every catalog Gemini with an upstream reference is enabled with its exact class', () => {
  const catalog = JSON.parse(readFileSync(new URL('../model_catalog.json', import.meta.url)));
  const classes = JSON.parse(readFileSync(new URL('./models.json', import.meta.url)));
  for (const entry of catalog.filter(p => p.model.startsWith('gemini-'))) {
    const id = entry.model === 'gemini-3.1-pro' ? 'gemini-3.1-pro-preview' : entry.model;
    assert.ok(reference.detector.model_ids.includes(id));
    assert.ok(models.includes(entry.model));
    const outputs = fixture[id], result = score(entry.model, outputs, reference);
    const original = analyzeSharedOutputs(outputs, reference.bank, reference.detector);
    assert.equal(classes[entry.model].class_id, id);
    assert.equal(result.status, 'match');
    assert.equal(result.expected_class, id);
    assert.equal(result.probability, original.results.find(r => r.model === id).probability);
    assert.deepEqual(result.candidates, original.results.slice(0,5));
    const mismatch = score(entry.model, fixture['gpt-6-astra'], reference);
    assert.equal(mismatch.status, 'mismatch');
    assert.ok(mismatch.probability < mismatch.candidates[0].probability);
  }
});

test('Gemini native challenges use sequential requests, low Argon thinking, and visible text only', async () => {
  let active = 0, peak = 0, calls = 0;
  const result = await detect({model:'gemini-4-argon',format:'gemini',base_url:'https://mock.example',key:'secret'},async(url,cfg,body)=>{
    assert.equal(url,'https://mock.example/v1beta/models/gemini-4-argon:streamGenerateContent?alt=sse');
    assert.deepEqual(body.generationConfig,{maxOutputTokens:8192,thinkingConfig:{thinkingLevel:'low'}});
    assert.equal(body.model,undefined); assert.ok(body.contents[0].parts[0].text.includes('355'));
    peak = Math.max(peak,++active);
    const text = fixture['gemini-4-argon'][calls++ % 3].text;
    await new Promise(resolve=>setTimeout(resolve,1)); active--;
    const payload = `data: ${JSON.stringify({candidates:[{index:0,content:{parts:[{text:'private thinking',thought:true}]}}]})}\r\n\r\ndata: ${JSON.stringify({candidates:[{index:0,content:{parts:[{text}]},finishReason:'STOP'}]})}\r\n\r\n`;
    const bytes = new TextEncoder().encode(payload);
    return new Response(new ReadableStream({start(controller){for(let i=0;i<bytes.length;i+=7)controller.enqueue(bytes.slice(i,i+7));controller.close()}}),{headers:{'Content-Type':'text/event-stream'}});
  });
  assert.equal(calls,3);assert.equal(peak,1);assert.equal(result.status,'match');
  assert.ok(!JSON.stringify(result).includes('private thinking'));
  for (const model of ['gemini-3.1-pro','gemini-3.8-flash']) {
    const collected = await collect({model,format:'gemini',base_url:'https://mock.example',key:'secret'},async(_url,_cfg,body)=>{
      assert.equal(body.generationConfig.thinkingConfig,undefined);
      return Response.json({candidates:[{content:{parts:[{text:'1 2 3'}]},finishReason:'STOP'}]});
    });
    assert.equal(collected.samples.filter(s=>s.state==='done').length,3);
  }
});

test('Gemini truncation, safety blocks, errors and reasoning-only outputs never become mismatch', async () => {
  const text = fixture['gemini-4-argon'][0].text;
  for (const payload of [
    {candidates:[{content:{parts:[{text}]}}]},
    {candidates:[{content:{parts:[{text}]},finishReason:'MAX_TOKENS'}]},
    {candidates:[{content:{parts:[{text,thought:true}]},finishReason:'STOP'}]},
    {candidates:[{content:{parts:[{functionCall:{name:'fake'}}]},finishReason:'STOP'}]},
    {promptFeedback:{blockReason:'SAFETY'}},
    {error:{message:'do not persist this'}}
  ]) {
    let calls=0;
    const result=await detect({model:'gemini-4-argon',format:'gemini',base_url:'https://mock.example',key:'secret'},async()=>{calls++;return new Response(`data: ${JSON.stringify(payload)}\n\n`,{headers:{'Content-Type':'text/event-stream'}})});
    assert.equal(calls,3);assert.equal(result.status,'error');assert.equal(result.probability,null);
    assert.ok(!JSON.stringify(result).includes('do not persist this'));
  }
  await assert.rejects(completeGemini({model:'gemini-4-argon',baseUrl:'https://mock.example',effort:'low'},'test',async()=>Response.json({error:{message:'private'}},{status:429})),e=>e.httpStatus===429);
});

test('Gemini Chat Completions compatibility preserves requested model and reference effort', async()=>{
  let calls=0;
  const result=await detect({model:'gemini-4-argon',format:'openai',base_url:'https://mock.example',key:'secret'},async(url,_cfg,body)=>{
    assert.equal(url,'https://mock.example/v1/chat/completions');assert.equal(body.model,'gemini-4-argon');
    assert.equal(body.reasoning_effort,'low');assert.equal(body.max_tokens,8192);
    return Response.json({choices:[{message:{content:fixture['gemini-4-argon'][calls++%3].text},finish_reason:'stop'}]});
  });
  assert.equal(calls,3);assert.equal(result.status,'match');
});
