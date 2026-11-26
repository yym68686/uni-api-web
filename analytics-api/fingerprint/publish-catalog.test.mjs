import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { deriveCatalog } from './publish-catalog.mjs';

test('derive profiles from explicit, separately verified sampling contracts', () => {
  const seed = JSON.parse(readFileSync(new URL('./models.json',import.meta.url)));
  const ids = [...new Set(Object.values(seed).map(m=>m.class_id)), 'gpt-new','gemini-4-hydrogen','gemini-alias-without-contract','gemini-wrong-model','unknown-format','ambiguous','nonstandard'];
  const bank = {models:ids.map(id=>({id,family:id.startsWith('gemini-')?'gemini':'gpt',sources:{'fixture/site':36},response_count:36}))};
  const detector = {model_ids:ids,response_counts:ids.map(()=>36)};
  const run = (id,change={}) => ({
    source:{channel:'fixture/site',endpoint:'https://never-executed.invalid'},
    model:{id},request:{model:id,format:'responses',stream:true,reasoning_effort:'low',...change},
    samples:[300,301,302].map(expected_count=>({completion:'complete',text:'synthetic evidence',expected_count})),
  });
  const catalog = deriveCatalog(bank,detector,[
    run('gpt-new'),run('unknown-format',{format:'unknown'}),
    run('gemini-4-hydrogen',{model:'google/gemini-4-hydrogen',format:'chat_completions',response_models:['google/gemini-4-hydrogen','google/gemini-4-hydrogen-20261112']}),
    run('gemini-alias-without-contract',{model:'google/gemini-alias-without-contract',format:'chat_completions',response_models:['google/other-model']}),
    run('gemini-wrong-model',{model:'google/gemini-other-model',format:'chat_completions',response_models:['google/gemini-other-model']}),
    {...run('ambiguous'),samples:run('ambiguous').samples.slice(0,2)},
    {...run('ambiguous',{format:'openai'}),samples:run('ambiguous').samples.slice(1)},
    run('nonstandard',{reasoning_effort:'high'}),
  ]);
  assert.equal(catalog.protocols['gpt-new'],'responses');
  assert.equal(catalog.models['gpt-new'].class_id,'gpt-new');
  assert.equal(catalog.protocols['gemini-4-argon'],'gemini');
  assert.equal(catalog.protocols['gemini-4-hydrogen'],'chat');
  assert.equal(catalog.models['gemini-4-hydrogen'].effort,'low');
  assert.deepEqual(catalog.held,['gemini-alias-without-contract','gemini-wrong-model','unknown-format','ambiguous','nonstandard']);
});

test('admit GLM, Messages, token budgets and declared aliases without accepting unverified samples', () => {
  const seed = JSON.parse(readFileSync(new URL('./models.json',import.meta.url)));
  const extra = {
    'glm-5.3':'glm','glm-5.3-flash':'glm','glm-wrong':'glm','glm-null-effort':'glm',
    'claude-opus-4.8':'claude','claude-opus-9.9':'claude','claude-sonnet-5.5':'claude','qwen-mini':'qwen',
    'glm-truncated':'glm','glm-bad-score':'glm','glm-random-knob':'glm','glm-bad-cap':'glm',
  };
  const ids = [...new Set(Object.values(seed).map(m=>m.class_id)),...Object.keys(extra)];
  const bank = {models:ids.map(id=>({id,family:extra[id] ?? 'gpt',sources:{fixture:36},response_count:36}))};
  const detector = {model_ids:ids,response_counts:ids.map(()=>36)};
  const run = (id,fields={},completion='complete') => ({
    source:{channel:'fixture'},model:{id},
    request:{model:`z-ai/${id}`,format:'chat_completions',stream:true,reasoning_effort:'',response_models:[`z-ai/${id}`],...fields},
    samples:[300,301,302].map(expected_count=>({completion,text:'fixture',expected_count})),
  });
  const catalog = deriveCatalog(bank,detector,[
    run('glm-5.3',{reasoning_effort:'low',max_tokens:8192,temperature:null},'unknown'),
    run('glm-5.3-flash'),
    run('glm-wrong',{response_models:['z-ai/glm-other']}),
    run('glm-null-effort',{reasoning_effort:null}),
    run('claude-opus-4.8',{format:'messages',reasoning_effort:'none',max_tokens:4096,
      model:'anthropic/claude-opus-4.8',response_models:['anthropic/claude-opus-4.8','claude-opus-4-8']}),
    run('claude-sonnet-5.5',{model:'anthropic/claude-sonnet-5.5',response_models:['anthropic/claude-sonnet-5.5']}),
    run('claude-opus-9.9',{model:'anthropic/claude-opus-9.9',response_models:['anthropic/claude-opus-9.9']}),
    run('qwen-mini',{model:'qwen/qwen-mini',reasoning_effort:'minimal'}),
    run('glm-truncated',{},'truncated'),run('glm-bad-score'),run('glm-random-knob',{temperature:0.5}),
    run('glm-bad-cap',{max_tokens:999999}),
  ],(id,samples)=>id!=='glm-bad-score' && samples.length===3);
  assert.equal(catalog.models['glm-5.3'].effort,'low');
  assert.equal(catalog.models['glm-5.3'].max_tokens,undefined);
  assert.equal(catalog.models['glm-5.3-flash'].effort,'default');
  assert.equal(catalog.models['claude-opus-4-8'].class_id,'claude-opus-4.8');
  assert.equal(catalog.models['claude-opus-4-8'].max_tokens,4096);
  assert.equal(catalog.protocols['claude-opus-4-8'],'messages');
  assert.equal(catalog.models['claude-sonnet-5-5'].class_id,'claude-sonnet-5.5');
  assert.equal(catalog.models['claude-opus-9-9'],undefined);
  assert.equal(catalog.models['qwen-mini'].effort,'minimal');
  assert.deepEqual(catalog.deferred,{
    'glm-wrong':'request_contract','glm-null-effort':'request_contract','glm-truncated':'reference_samples',
    'glm-bad-score':'offline_score','glm-random-knob':'request_contract','glm-bad-cap':'request_contract',
  });
});

test('prefer completed evidence and preserve a previously supported sampling mode when still usable', () => {
  const seed = JSON.parse(readFileSync(new URL('./models.json',import.meta.url)));
  const ids = [...new Set(Object.values(seed).map(m=>m.class_id)),'glm-example'];
  const bank = {models:ids.map(id=>({id,family:'glm',sources:{fixture:36},response_count:36}))};
  const detector = {model_ids:ids,response_counts:ids.map(()=>36)};
  const run = (reasoning_effort, completion) => ({
    source:{channel:'fixture'},model:{id:'glm-example'},
    request:{model:'glm-example',format:'chat_completions',stream:true,reasoning_effort},
    samples:[300,301,302].map(expected_count=>({completion,text:'fixture',expected_count})),
  });
  const runs = [run('low','unknown'),run('','complete')];
  assert.equal(deriveCatalog(bank,detector,runs).models['glm-example'].effort, 'default');
  const existing = {models:{'glm-example':{class_id:'glm-example',effort:'low'}}, protocols:{'glm-example':'chat'}};
  assert.equal(deriveCatalog(bank,detector,runs,undefined,existing).models['glm-example'].effort, 'low');
});
