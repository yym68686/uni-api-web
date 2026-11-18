import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { deriveCatalog } from './publish-catalog.mjs';

test('derive profiles from explicit metadata; hold ambiguous and unimplemented contracts', () => {
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
    run('gpt-new'),run('unknown-format',{format:'anthropic'}),
    run('gemini-4-hydrogen',{model:'google/gemini-4-hydrogen',format:'chat_completions',response_models:['google/gemini-4-hydrogen','google/gemini-4-hydrogen-20261112']}),
    run('gemini-alias-without-contract',{model:'google/gemini-alias-without-contract',format:'chat_completions',response_models:['google/other-model']}),
    run('gemini-wrong-model',{model:'google/gemini-other-model',format:'chat_completions',response_models:['google/gemini-other-model']}),
    run('ambiguous'),run('ambiguous',{format:'openai'}),
    run('nonstandard',{reasoning_effort:'high'}),
  ]);
  assert.equal(catalog.protocols['gpt-new'],'responses');
  assert.equal(catalog.models['gpt-new'].class_id,'gpt-new');
  assert.equal(catalog.protocols['gemini-4-argon'],'gemini');
  assert.equal(catalog.protocols['gemini-4-hydrogen'],'chat');
  assert.equal(catalog.models['gemini-4-hydrogen'].effort,'low');
  assert.deepEqual(catalog.held,['gemini-alias-without-contract','gemini-wrong-model','unknown-format','ambiguous','nonstandard']);
});
