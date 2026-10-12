import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { collect, detect, loadReference, score, scoreCollected } from './runner.mjs';
import { analyzeSharedOutputs } from './upstream/shared-detector.ts';
import { generateChallenges } from './upstream/challenge-browser.js';
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
  collected.samples[0].state='error';
  assert.equal(scoreCollected(collected,reference).status,'error');
  assert.equal(scoreCollected(collected,reference).probability,null);
});

test('full reference bank, original scoring and the merged Astra/Sol class are preserved', () => {
  assert.equal(reference.bank.models.length, 57);
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
  let calls=0;
  const transport=async(_url,config,body)=>{
    calls++;assert.equal(config.model,'gpt-6.1-sol');assert.deepEqual(body.reasoning,{effort:'low'});
    assert.equal(body.stream,true);assert.equal(body.store,false);assert.equal(body.max_output_tokens,8192);
    assert.ok(body.input[0].content.length>300);
    if(calls===2)return Response.json({error:{message:'secret-key should not persist'}},{status:429});
    return Response.json({status:'completed',output:[{type:'message',role:'assistant',content:[{type:'output_text',text:fixture['gpt-6-astra'][0].text}]}]});
  };
  const result=await detect({base_url:'https://mock.example',key:'secret-key',model:'gpt-6.1-sol'},transport);
  assert.equal(calls,3);assert.equal(result.status,'error');assert.equal(result.probability,null);
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
