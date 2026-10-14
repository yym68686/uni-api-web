import { expect, it } from "vitest";
import { fingerprintKey, sharedFingerprintIndex } from "./fingerprintResults";
import type { FingerprintRecord } from "./ModelFingerprints";
import type { ManagedChannel } from "./channelManagement";

const fingerprintChannel = {
  source_id:"fugue", provider:"native", models:["gemini-4-argon"],
  probe_fingerprint:"current", kind:"configured", binding_status:"matched",
  bound_keys:[{account_id:"one",group_id:1}], model_mappings:{"gemini-4-argon":"gemini-4-argon"},
} as unknown as ManagedChannel;
const site: FingerprintRecord = {account_id:"one",group_id:1,model:"gemini-4-argon",state:"done",message:"",revision:"site-key",updated_at_ms:10000,result:{model:"gemini-4-argon",status:"match",probability:.95,prediction:"gemini-4-argon",prediction_name:"gemini-4-argon",expected_class:"gemini-4-argon",used_outputs:3,checked_at:10,reference_commit:"ref",candidates:[]}};
const native: FingerprintRecord = {...site,account_id:undefined,group_id:undefined,source_id:"fugue",provider:"native",revision:"current",updated_at_ms:20000,result:{...site.result!,status:"mismatch",probability:.1,checked_at:20}};

it("shares the latest result in both directions without changing its original identity",()=>{
  let index=sharedFingerprintIndex([site],[fingerprintChannel]);
  expect(index.get(fingerprintKey(native))).toBe(site);
  index=sharedFingerprintIndex([site,native],[fingerprintChannel]);
  expect(index.get(fingerprintKey(site))).toBe(native);
  expect(index.get(fingerprintKey(native))).toBe(native);
  const newerSite={...site,updated_at_ms:21000};
  index=sharedFingerprintIndex([newerSite,native],[fingerprintChannel]);
  expect(index.get(fingerprintKey(native))).toBe(newerSite);
  expect(native.revision).toBe("current");
});
it("shares pending jobs and newer failures instead of leaving stale success visible",()=>{
  const pending={...native,state:"running",revision:"",result:null};
  let index=sharedFingerprintIndex([site,pending],[fingerprintChannel]);
  expect(index.get(fingerprintKey(site))).toBe(pending);
  const failed={...native,state:"error",result:null};
  index=sharedFingerprintIndex([site,failed],[fingerprintChannel]);
  expect(index.get(fingerprintKey(site))).toBe(failed);
});
it("does not share changed credentials, ambiguous groups, aliases or unavailable sources",()=>{
  const stale={...native,revision:"old-key"};
  expect(sharedFingerprintIndex([stale],[fingerprintChannel]).get(fingerprintKey(site))).toBeUndefined();
  for(const channel of [
    {...fingerprintChannel,binding_status:"partial" as const},
    {...fingerprintChannel,binding_status:"ambiguous" as const},
    {...fingerprintChannel,probe_fingerprint:""},
    {...fingerprintChannel,model_mappings:{"gemini-4-argon":"gpt-6-astra"}},
    {...fingerprintChannel,bound_keys:[...fingerprintChannel.bound_keys!,{account_id:"two",group_id:2}]},
  ] as ManagedChannel[]){
    expect(sharedFingerprintIndex([site],[channel]).get(fingerprintKey(native))).toBeUndefined();
    expect(sharedFingerprintIndex([native],[channel]).get(fingerprintKey(site))).toBeUndefined();
  }
  expect(sharedFingerprintIndex([site],[fingerprintChannel],["fugue"]).get(fingerprintKey(native))).toBeUndefined();
  expect(sharedFingerprintIndex([{...site,account_id:"other"}],[fingerprintChannel]).get(fingerprintKey(native))).toBeUndefined();
  expect(sharedFingerprintIndex([{...site,model:"gpt-6-astra"}],[fingerprintChannel]).get(fingerprintKey(native))).toBeUndefined();
});

it("shares generated imports using exact live ownership plus the current credential fingerprint",()=>{
  const managed={...fingerprintChannel,source_name:"Fugue",binding_status:undefined,bound_keys:undefined};
  const imported={...managed,kind:undefined,account_id:"one",group_id:1} as ManagedChannel;
  expect(sharedFingerprintIndex([site],[managed],[],[imported]).get(fingerprintKey(native))).toBe(site);
  expect(sharedFingerprintIndex([native],[managed],[],[imported]).get(fingerprintKey(site))).toBe(native);
  expect(sharedFingerprintIndex([{...native,revision:"old"}],[managed],[],[imported]).get(fingerprintKey(site))).toBeUndefined();
  for(const imports of [
    [{...imported,provider:"other"}],
    [{...imported,source_id:"other"}],
    [{...imported,models:[]}],
    [{...imported,model_mappings:{"gemini-4-argon":"gpt-6-astra"}}],
    [imported,{...imported,account_id:"other"}],
  ]) expect(sharedFingerprintIndex([site],[managed],[],imports).get(fingerprintKey(native))).toBeUndefined();
  expect(sharedFingerprintIndex([site],[managed],["Fugue"],[imported]).get(fingerprintKey(native))).toBeUndefined();
  expect(sharedFingerprintIndex([site],[managed],[],[imported],["Fugue"]).get(fingerprintKey(native))).toBeUndefined();
});
