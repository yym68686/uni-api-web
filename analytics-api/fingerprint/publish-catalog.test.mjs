import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { deriveCatalog } from './publish-catalog.mjs';

test('derive profiles from explicit metadata; hold ambiguous and unimplemented contracts', () => {
  const seed = JSON.parse(readFileSync(new URL('./models.json',import.meta.url)));
  const ids = [...new Set(Object.values(seed).map(m=>m.class_id)), 'gpt-new','unknown-format','ambiguous','nonstandard'];
  const bank = {models:ids.map(id=>({id,family:'gpt',sources:{'fixture/site':36},response_count:36}))};
  const detector = {model_ids:ids,response_counts:ids.map(()=>36)};
  const run = (id,change={}) => ({
    source:{channel:'fixture/site',endpoint:'https://never-executed.invalid'},
    model:{id},request:{model:id,format:'responses',stream:true,reasoning_effort:'low',...change},
    samples:[300,301,302].map(expected_count=>({completion:'complete',text:'synthetic evidence',expected_count})),
  });
  const catalog = deriveCatalog(bank,detector,[
    run('gpt-new'),run('unknown-format',{format:'anthropic'}),
    run('ambiguous'),run('ambiguous',{format:'openai'}),
    run('nonstandard',{reasoning_effort:'high'}),
  ]);
  assert.equal(catalog.protocols['gpt-new'],'responses');
  assert.equal(catalog.models['gpt-new'].class_id,'gpt-new');
  assert.equal(catalog.protocols['gemini-4-argon'],'gemini');
  assert.deepEqual(catalog.held,['unknown-format','ambiguous','nonstandard']);
});
