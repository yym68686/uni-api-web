import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { afterEach, expect, it, vi } from "vitest";
import { FingerprintPanel, FingerprintStatus, FINGERPRINT_MODELS, fingerprintProbabilityClass, fingerprintTargets, fingerprintKey, rowFingerprints } from "./ModelFingerprints";
import type { FingerprintRecord } from "./ModelFingerprints";
import type { ManagementRow } from "./channelManagement";
import { Sub2apiChecks } from "./Sub2apiChecks";
import { loadSubFilters, saveSubFilters, subFilterDefaults } from "./sub2apiPreferences";
afterEach(()=>vi.unstubAllGlobals());
const record: FingerprintRecord = { account_id:"one",group_id:1,model:"gpt-6-astra",state:"done",message:"",revision:"r1",result:{model:"gpt-6-astra",status:"mismatch",probability:.12,prediction:"claude-haiku-4.5",prediction_name:"Claude Haiku 4.5",expected_class:"gpt-6-astra/6.1-sol",used_outputs:3,checked_at:1,reference_commit:"abc",candidates:[]} };
function mount(view: React.ReactNode){return render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><Tooltip.Provider>{view}</Tooltip.Provider></QueryClientProvider>)}
it("shows requested-class probability below mismatch with the same quality colors and unknown as dash",()=>{
 const view=mount(<FingerprintStatus record={record}/>);
 expect(screen.getByText("不匹配")).toBeInTheDocument();expect(screen.getByText("12.0%")).toHaveClass("quality-poor");
 view.rerender(<Tooltip.Provider><FingerprintStatus record={{...record,state:"error"}}/></Tooltip.Provider>);
 expect(screen.getByText("检测失败")).toBeInTheDocument();expect(screen.getByText("—")).toHaveClass("quality-unknown");
 expect(fingerprintProbabilityClass(1)).toBe("quality-perfect");expect(fingerprintProbabilityClass(.9)).toBe("quality-partial");expect(fingerprintProbabilityClass(.899)).toBe("quality-poor");
});
it("only selects the supported models and fences configured stale evidence",()=>{
 const row={account:{id:""},target:{group_id:0},configured:{source_id:"fugue",provider:"native",models:["gpt-6-astra"],probe_fingerprint:"new"}} as ManagementRow;
 const targets=fingerprintTargets(row,["gpt-6-astra","gpt-6.1-sol","gpt-5.5"]);expect(targets).toHaveLength(2);
 const native={...record,account_id:undefined,group_id:undefined,source_id:"fugue",provider:"native"};
 expect(rowFingerprints(row,new Map([[fingerprintKey(native),native]]),"gpt-6-astra")[0].record).toBeUndefined();
 expect(rowFingerprints(row,new Map(),"gpt-5.5")).toEqual([]);
});
it("enables each supported Gemini with its own probability and target",()=>{
 const row={account:{id:"one"},target:{group_id:1}} as ManagementRow;
 for(const model of ["gemini-3.1-pro","gemini-3.8-flash","gemini-4-argon"]){
  expect(FINGERPRINT_MODELS).toContain(model);
  expect(fingerprintTargets(row,[model,"gpt-5.5"])).toEqual([{account_id:"one",group_id:1,model}]);
  const gemini={...record,model,result:{...record.result!,model,expected_class:model,probability:.95}};
  const view=mount(<FingerprintStatus record={gemini}/>);
  expect(screen.getByText("不匹配")).toBeInTheDocument();
  expect(screen.getByText("95.0%")).toHaveClass("quality-partial");
  view.unmount();
 }
});
it("queues/cancels only fingerprint jobs and retains independent legacy detection",async()=>{
 let current:FingerprintRecord|undefined;const writes:unknown[]=[];
 vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
  expect(input).toContain("/v1/model-fingerprints");
  if(init?.method){writes.push(JSON.parse(init.body as string));current={...record,state:init.method==="DELETE"?"interrupted":"queued",result:null};return Response.json({accepted:true})}
  return Response.json({data:current?[current]:[]});
 }));
 mount(<FingerprintPanel targets={[{account_id:"one",group_id:1,model:"gpt-6-astra"}]}/>);
 const user=userEvent.setup();await waitFor(()=>expect(screen.getByRole("button",{name:"检测指纹"})).toBeEnabled());
 await user.click(screen.getByRole("button",{name:"检测指纹"}));await screen.findByRole("button",{name:"取消检测"});
 await user.click(screen.getByRole("button",{name:"取消检测"}));await screen.findByText("已中断");
 expect(writes).toEqual(Array(2).fill({targets:[{account_id:"one",group_id:1,model:"gpt-6-astra"}]}));
});
it.each(["gpt-6-astra","gemini-3.1-pro","gemini-3.8-flash","gemini-4-argon"])("channel management filters %s fingerprints independently of quality",async(model)=>{
 const scoped={...record,model,result:{...record.result!,model}};
 const accounts=["one","two"].map(id=>({id,name:id,base:`https://${id}.test`,email:"test",state:"idle",message:"",synced_at:1,targets:[{group_id:1,name:`group-${id}`,platform:"openai",channel:id,rate:1,key_id:1,active:true,state:"done",message:"",result:null}]}));
 vi.stubGlobal("fetch",vi.fn(async(input:string)=>Response.json({data:input.endsWith("/accounts")?accounts:input.endsWith("/model-fingerprints")?[scoped,{...scoped,account_id:"two",result:{...scoped.result,status:"match",probability:1}}]:[],labels:{},unavailable_sources:[]})));
 mount(<Sub2apiChecks user={`fingerprint-filter-${model}`}/>);await screen.findByText("group-one",{selector:"strong"});
 expect(screen.getByRole("columnheader",{name:/模型指纹/})).toBeInTheDocument();
 const user=userEvent.setup();await user.selectOptions(screen.getByLabelText("检测模型筛选"),model);
 await user.selectOptions(screen.getByLabelText("模型指纹筛选"),"mismatch");
 await waitFor(()=>expect(screen.queryByText("group-two",{selector:"strong"})).not.toBeInTheDocument());
 expect(screen.getByText("group-one",{selector:"strong"})).toBeInTheDocument();expect(screen.getByText("12.0%")).toBeInTheDocument();
 await user.selectOptions(screen.getByLabelText("检测模型筛选"),"gpt-6.1-sol");
 await waitFor(()=>expect(screen.queryByText("group-one",{selector:"strong"})).not.toBeInTheDocument());
});
it("persists and validates fingerprint filters",()=>{
 saveSubFilters("fp-test",{...subFilterDefaults,fingerprint:"mismatch"});expect(loadSubFilters("fp-test").fingerprint).toBe("mismatch");
 saveSubFilters("fp-test",{...subFilterDefaults,fingerprint:"bogus"});expect(loadSubFilters("fp-test").fingerprint).toBe("");
});

it("shows backend rollout unavailability without reporting untested or submitting paid requests",async()=>{
 vi.stubGlobal("fetch",vi.fn(async()=>new Response("not found",{status:404})));
 mount(<FingerprintPanel targets={[{source_id:"fugue",provider:"native",model:"gpt-6-astra"}]}/>);
 await screen.findByText("指纹检测服务待更新");
 expect(screen.getByRole("button",{name:"检测指纹"})).toBeDisabled();
 expect(screen.queryByText("未检测")).not.toBeInTheDocument();
});
