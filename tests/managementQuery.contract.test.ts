import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { gzipSync, gunzipSync } from "node:zlib";
import { expect, it } from "vitest";
import { managementRows } from "../src/channelManagement";
import type { ManagedChannel, ConfiguredCheck } from "../src/channelManagement";
import type { SubAccount, SubTarget, Result } from "../src/Sub2apiChecks";
import type { InstalledChannel } from "../src/sub2apiImports";
import { filterManagementRows } from "../src/managementQueryFilters";
import { subFilterDefaults } from "../src/sub2apiPreferences";
import type { SubFilters } from "../src/sub2apiPreferences";
import type { ModelPrice } from "../src/types";
import { sharedFingerprintIndex } from "../src/fingerprintResults";
import { rowFingerprints, fingerprintStatus } from "../src/ModelFingerprints";
import type { FingerprintRecord } from "../src/ModelFingerprints";
import { managementJoinState } from "../src/channelOptimizationPlan";
import type { Routes } from "../src/channelRouteData";

// The existing browser implementation is the independent compatibility oracle
// for the upcoming server query. Synthetic data only; no production requests.
const result = (model: string, at: number, status = "success", verdict = "pass"): Result => ({
  model, checked_at: at, verdict,
  availability: { status, text: "", ttft_ms: null, duration_ms: 1, model_match: status === "success" ? "match" : "unavailable" },
  quality: { status: "success", text: "21", ttft_ms: null, duration_ms: 1 },
});
const target = (group: number, patch: Partial<SubTarget> = {}): SubTarget => ({
  group_id: group, name: `group-${group}`, channel: "", platform: "openai", rate: 1,
  key_id: group, active: true, state: "done", message: "", result: result("gpt-6-astra", 5),
  history: {total: 3, successful: 2, passed: 1}, billing: {rate: 1, source: "key", checked_at: 1},
  models: [
    {model: "actual", state: "done", message: "", result: result("actual", 30)},
    {model: "gpt-6-astra", state: "done", message: "", result: result("gpt-6-astra", 10)},
    {model: "gpt-5.5", state: "done", message: "", result: result("gpt-5.5", 20)},
  ],
  tool_use: {status: "supported", checked_at: 10, attempts: [], models: [{model:"actual",state:"done",result:{model:"actual",status:"supported",checked_at:10,attempts:[]}}]},
  ...patch,
});
const account = (id: string, targets: SubTarget[]): SubAccount => ({
  id, name:id, base:`https://${id}.example/tenant`, email:`${id}@example.test`,state:"idle",message:"",synced_at:1,targets,
});
const channel = (source: string, provider: string, patch: Partial<ManagedChannel> = {}): ManagedChannel => ({
  source_id:source, source_name:source, provider, name:provider, engine:"gpt", kind:"configured",
  account_id:"", group_id:0, account_ids:[], base:"https://fixture.example", api_key_id:"",key_position:0,key_prefix:"",
  positions:{},revision:"r1",manageable:true,models:["public"],model_mappings:{public:"actual"},probe_fingerprint:`${source}-current`,
  ...patch,
});
const check = (source: string, provider: string, model: string, patch: Partial<ConfiguredCheck> = {}): ConfiguredCheck => ({
  source_id:source,provider,model,kind:"model",fingerprint:`${source}-current`,state:"done",message:"",
  result:result(model,15),history:{total:4,successful:3,passed:2},...patch,
});
const a = account("a", [target(1), target(2,{models:[],active:false,state:"error",result:null})]);
const b = account("b", [target(1,{models:[{model:"actual-b",state:"running",message:"queued-new",result:result("actual-b",40,"error")}],compaction:{status:"supported",model:"gpt-5.5",checked_at:50,attempts:[]}})]);
const sources = [
  channel("one","multi",{account_id:"a",group_id:1,account_ids:["a"]}),
  channel("two","multi",{account_id:"b",group_id:1,account_ids:["b"],model_mappings:{public:"actual-b"},models:["public","gpt-5.5"],base:"http://private.example"}),
  channel("one","orphan",{account_ids:["a"],models:["gpt-6-astra","gpt-5.5","custom"]}),
  channel("one","imported",{account_id:"a",group_id:1,account_ids:["a"]}),
  channel("one","native-unlinked",{account_ids:["a"]}),
  channel("one","multi",{engine:"claude",models:["claude-opus-5-5"]}),
  channel("one","legacy-capability",{models:["gpt-5.5"]}),
  channel("one","",{}),
];
const inputs = {
  accounts:[a,b], channels:sources,
  imports:[{...sources[3],kind:undefined,api_key_id:"caller",models:["public"]}] as InstalledChannel[],
  checks:[
    check("one","multi","public",{result:result("public",100),fingerprint:"old"}),
    check("two","multi","public",{result:result("public",45)}),
    check("one","multi","custom",{state:"queued",result:null}),
    check("two","multi","",{kind:"compaction",state:"running",result:{status:"error",checked_at:20,attempts:[]}}),
    check("one","orphan","gpt-6-astra",{state:"queued",result:result("gpt-6-astra",5,"error","fail")}),
    check("one","orphan","gpt-5.5",{kind:"availability",result:result("gpt-5.5",20)}),
    check("one","orphan","custom",{result:result("custom",20)}),
    check("one","orphan","gpt-5.5",{kind:"tool-use",state:"running",result:null,message:"working"}),
    check("one","legacy-capability","",{kind:"tool-use",result:{status:"supported",model:"gpt-5.5",checked_at:20,attempts:[]}}),
    check("one","native-unlinked","",{kind:"compaction",result:null,message:"not available"}),
  ],
};
const cases = [
  {name:"empty",input:{accounts:[],channels:[],imports:[],checks:[]},model:"",account:""},
  ...["","public","actual","gpt-5.5","gpt-6-astra","custom","missing"].flatMap(model=>
    ["","a","b","__unassigned__","missing"].map(selected=>({name:`model=${model}/account=${selected}`,input:inputs,model,account:selected}))),
  {name:"legacy-empty-fingerprint",input:{...inputs,channels:inputs.channels.map(c=>({...c,probe_fingerprint:""}))},model:"public",account:""},
  {name:"empty-model-overrides-legacy",input:{...inputs,accounts:[account("empty",[target(1,{models:[{model:"gpt-6-astra",state:"running",message:"",result:null}]})])]},model:"gpt-6-astra",account:""},
];
const expected = cases.map(c=>({ ...c, expected:managementRows(c.input.accounts,c.input.channels,c.input.imports,c.model,c.account,c.input.checks)
  .map(row=>({...row,account:{...row.account,targets:[]}})) }));
const fixturePath = resolve("analytics-api/testdata/management-query-rows.json.gz");

it("freezes the browser row contract for server-side filtering parity",()=>{
  if(process.env.UPDATE_MANAGEMENT_QUERY_FIXTURES === "1") {
    mkdirSync(dirname(fixturePath),{recursive:true});
    writeFileSync(fixturePath,gzipSync(Buffer.from(JSON.stringify(expected)),{level:9}));
  }
  expect(JSON.parse(gunzipSync(readFileSync(fixturePath)).toString("utf8"))).toEqual(JSON.parse(JSON.stringify(expected)));
});

it("freezes every filter, facets and stable sort against the browser implementation",()=>{
  const priceInputs=structuredClone(inputs);
  const receipt=(input:number,output:number,tokens=100)=>({status:"matched",input_price:input,output_price:output,input_tokens:tokens,output_tokens:100,cache_read_tokens:0,cache_creation_tokens:0});
  for(const account of priceInputs.accounts)for(const target of account.targets)for(const check of target.models || []) {
    if(check.result)check.result.availability.usage=receipt(account.id==="a"?1:3,2) as never;
  }
  priceInputs.accounts.push(account("unicode",[
    target(1,{name:"İ ΟΣ 😀",platform:"\uE000",billing:{rate:0,source:"key",checked_at:1},observed_rates:{"gpt-5.5":{rate:0,observed_at:2}}}),
    target(2,{platform:"😀",billing:{rate:.9,source:"key",checked_at:1},observed_rates:{"gpt-5.5":{rate:1.1,observed_at:2},actual:{rate:0,observed_at:2}}}),
  ]));
  const prices=[{model:"actual",input:1,output:2,verified:true,source:"manual"},
    {model:"actual-b",input:1,output:2,verified:false,source:"manual"},
    {model:"gpt-5.5",input:1,output:2,verified:true,source:"manual"},
    {model:"public",input:1,output:2,verified:false,source:"manual"}] as ModelPrice[];
  const patches:Partial<SubFilters>[]=[{},
    ...["a","private","İ","οσ","ος","i\u0307","😀","no-match"].map(search=>({search})),
    ...["a","b","__unassigned__","missing"].map(accountId=>({accountId})),
    ...["0","0.9","1","1.1","mismatch"].map(maxRate=>({maxRate})),
    ...["asc","desc"].map(sort=>({sort})),
    ...["success","error","untested"].map(availability=>({availability})),
    ...["available","unavailable","all"].map(groupStatus=>({groupStatus})),
    ...["normal","abnormal","unconfirmed","unconfirmed_match","unconfirmed_mismatch"].map(priceStatus=>({priceStatus})),
    ...["supported","unsupported","error","untested","not_applicable"].flatMap(status=>[{toolUse:status},{compaction:status}]),
    ...["pass","fail","inconclusive","error"].map(quality=>({quality})),
    ...["0","50","100"].map(minQuality=>({minQuality})),
    ...["openai","claude","😀","\uE000"].map(platform=>({platform})),
    ...["joined","unjoined"].map(joined=>({joined})),
    ...["match","mismatch","legacy","unavailable","untested"].map(modelMatch=>({modelMatch})),
    ...["match","mismatch","untested","not_applicable"].map(fingerprint=>({fingerprint})),
    {maxRate:"1",sort:"asc",availability:"success",minQuality:"50"},
    {maxRate:"0.9",platform:"openai",joined:"joined",fingerprint:"match"},
  ];
  const filterCases=["","public","gpt-5.5","claude-opus-5-5"].flatMap(model=>patches.map(patch=>{
    // Preserve the frozen contract's explicit configuration order; the UI default is tested separately.
    const filters={...subFilterDefaults,sort:"",model,...patch};
    const rows=managementRows(priceInputs.accounts,priceInputs.channels,priceInputs.imports,model,filters.accountId,priceInputs.checks);
    const fingerprints=Object.fromEntries(rows.map((row,i)=>[row.id,[[],["match","untested"],["mismatch"],["untested"]][i%4]]));
    const joined=Object.fromEntries(rows.map((row,i)=>[row.id,["joined","unjoined","unknown"][i%3]]));
    const output=filterManagementRows(rows,filters,{prices,fingerprintReady:true,fingerprints:row=>fingerprints[row.id],joined:row=>joined[row.id]});
    return {filters,evidence:{prices,fingerprint_ready:true,fingerprints,joined},expected:{ids:output.rows.map(row=>row.id),rates:output.rates,platforms:output.platforms}};
  }));
  const data={input:priceInputs,cases:filterCases};
  const path=resolve("analytics-api/testdata/management-query-filters.json.gz");
  if(process.env.UPDATE_MANAGEMENT_QUERY_FIXTURES==="1")writeFileSync(path,gzipSync(Buffer.from(JSON.stringify(data)),{level:9}));
  expect(JSON.parse(gunzipSync(readFileSync(path)).toString("utf8"))).toEqual(JSON.parse(JSON.stringify(data)));
});

it("freezes shared fingerprint identity and actual caller-key join evidence",()=>{
  const record=(patch:Partial<FingerprintRecord>):FingerprintRecord=>({model:"gpt-6-astra",state:"done",message:"",revision:"one-current",updated_at_ms:100,result:{model:"gpt-6-astra",status:"match",probability:1,prediction:"",prediction_name:"",expected_class:"",used_outputs:3,checked_at:1,reference_commit:"fixture",candidates:[]},...patch});
  const base=structuredClone(inputs);
  base.channels=[channel("one","shared",{models:["gpt-6-astra","gpt-6.1-sol"],model_mappings:{},account_id:"a",group_id:1,account_ids:["a"],binding_status:"matched",bound_keys:[{account_id:"a",account_name:"a",base:"https://a.test",group_id:1,remote_key_id:1}]}),
    channel("two","shared",{models:["gpt-6-astra"],model_mappings:{},account_ids:[]}),channel("one","unbound",{models:["gpt-6-astra"],model_mappings:{}})];
  base.imports=[{...base.channels[0],kind:undefined,api_key_id:"caller",models:["gpt-6-astra","gpt-6.1-sol"]}];
  const records=[record({account_id:"a",group_id:1,revision:"site",updated_at_ms:50}),
    record({source_id:"one",provider:"shared",updated_at_ms:200}),record({source_id:"two",provider:"shared",revision:"two-old",updated_at_ms:300}),
    record({source_id:"one",provider:"unbound",revision:"old",updated_at_ms:400}),record({account_id:"a",group_id:1,model:"gpt-6.1-sol",state:"queued",updated_at_ms:10,result:null})];
  const routes={one:{data:[{provider:"copy",origin_provider:"unbound",api_key_id:"caller"}],unavailable_keys:[],snapshot_consistent:true},two:{data:[],unavailable_keys:[],snapshot_consistent:true}} as Record<string,Routes>;
  const root={...base,fingerprints:records,fingerprint_ready:true,unavailable_sources:[] as string[],unavailable_imports:[] as string[],routes};
  const variants=[
    root,
    {...root,fingerprints:records.map((r,i)=>i===1?{...r,updated_at_ms:0}:r)},
    {...root,fingerprints:records.map((r,i)=>i===1?{...r,updated_at_ms:undefined}:r)},
    {...root,fingerprints:records.map((r,i)=>i===1?{...r,state:"running",revision:"old",result:null}:r)},
    {...root,unavailable_sources:["one"]},
    {...root,unavailable_imports:["one"]},
    {...root,imports:undefined},
    {...root,routes:{}},
    {...root,routes:{one:{data:[],unavailable_keys:["caller"],snapshot_consistent:false}}},
    {...root,channels:root.channels.map(c=>({...c,model_mappings:{"gpt-6-astra":"different"}}))},
    {...root,imports:[...root.imports,{...root.imports[0],account_id:"b"}]},
  ];
  const data=variants.flatMap(input=>["","gpt-6-astra","gpt-6.1-sol","custom"].map(model=>{
    const rows=managementRows(input.accounts,input.channels,input.imports || [],model,"",input.checks);
    const index=sharedFingerprintIndex(input.fingerprints,input.channels,input.unavailable_sources,input.imports || [],input.unavailable_imports);
    return {input,model,expected:{prices:[],fingerprint_ready:true,
      fingerprints:Object.fromEntries(rows.map(row=>[row.id,rowFingerprints(row,index,model).map(e=>fingerprintStatus(e.record))])),
      joined:Object.fromEntries(rows.map(row=>[row.id,managementJoinState(row,input.imports?{data:input.imports,labels:{},unavailable_sources:input.unavailable_imports}:undefined,input.routes)]))}};
  }));
  const path=resolve("analytics-api/testdata/management-query-evidence.json.gz");
  if(process.env.UPDATE_MANAGEMENT_QUERY_FIXTURES==="1")writeFileSync(path,gzipSync(Buffer.from(JSON.stringify(data)),{level:9}));
  expect(JSON.parse(gunzipSync(readFileSync(path)).toString("utf8"))).toEqual(JSON.parse(JSON.stringify(data)));
});
