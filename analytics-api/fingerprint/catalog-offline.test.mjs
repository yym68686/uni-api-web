import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { collect, loadReference, score } from './runner.mjs';

test('every enabled profile scores three references and emits its exact request contract', {
  skip:!process.env.FINGERPRINT_DATA_DIR,
}, async () => {
  const read = name => JSON.parse(readFileSync(join(process.env.FINGERPRINT_DATA_DIR,name)));
  const reference = loadReference(), fixtures = read('reference-fixture.json');
  const protocols = read('protocols.json'), models = read('models.json');
  for (const [model,profile] of Object.entries(models)) {
    const examples = fixtures[profile.class_id];
    const result = score(model,examples,reference);
    assert.equal(result.used_outputs,3,model);
    assert.equal(result.status,'match',`${model}: ${result.prediction}`);
    const protocol = protocols[model], effort = profile.effort==='default' ? undefined : profile.effort;
    let calls = 0;
    const round = await collect({model,base_url:'http://example.test',key:'fixture'},async (url,_,body)=>{
      const path = new URL(url).pathname;
      const text = examples[calls++].text;
      if (protocol==='gemini') {
        assert.equal(path,`/v1beta/models/${model}:streamGenerateContent`);
        assert.equal(body.generationConfig.thinkingConfig?.thinkingLevel,effort);
        assert.equal(body.reasoning,undefined);
        return Response.json({candidates:[{content:{parts:[{text}]},finishReason:'STOP'}]});
      }
      assert.equal(body.model,model);
      assert.equal(body.stream,true);
      if (protocol==='chat') {
        assert.equal(path,'/v1/chat/completions');
        assert.equal(body.reasoning_effort,effort);
        return Response.json({choices:[{message:{content:text},finish_reason:'stop'}]});
      }
      assert.equal(path,'/v1/responses');
      assert.equal(body.reasoning?.effort,effort);
      return Response.json({status:'completed',output:[{type:'message',content:[{type:'output_text',text}]}]});
    });
    assert.equal(calls,3,model);
    assert.ok(round.samples.every(s=>s.state==='done'),`sampling/parse failed for ${model}`);
  }
});
