import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { afterEach, expect, it, vi } from "vitest";
import { FingerprintPanel, FingerprintStatus, FINGERPRINT_MODELS, fingerprintProbabilityClass, fingerprintTargets, fingerprintKey, rowFingerprints } from "./ModelFingerprints";
import type { FingerprintRecord } from "./ModelFingerprints";
import type { ManagedChannel, ManagementRow } from "./channelManagement";
import { Sub2apiChecks } from "./Sub2apiChecks";
import { loadSubFilters, saveSubFilters, subFilterDefaults } from "./sub2apiPreferences";
import { installManagementPageFixture } from "../tests/managementPageFixture";
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
it("does not report match or mismatch for a rejected numeric sequence",()=>{
 mount(<FingerprintStatus record={{...record,result:{...record.result!,status:"insufficient",decision:"not_model",probability:null,candidates:[],prediction:"",prediction_name:"不像语言模型回答"}}}/>);
 expect(screen.getByText("无法判定")).toBeInTheDocument();
 expect(screen.getByText("—")).toHaveClass("quality-unknown");
 expect(screen.queryByText("不匹配")).not.toBeInTheDocument();
 expect(screen.queryByText("12.0%")).not.toBeInTheDocument();
});
it("enables each supported Gemini with its own probability and target",()=>{
 const row={account:{id:"one"},target:{group_id:1}} as ManagementRow;
 for(const model of ["gemini-3.1-pro","gemini-3.8-flash","gemini-4-argon","gemini-4-carbon"]){
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
  if(input.endsWith("/channel-management"))return Response.json({data:[],unavailable_sources:[]});
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
it.each(["done","running","queued","interrupted","error"].flatMap(state=>[1,2].map(count=>({state,count}))))("shows provisional ranking for $count samples while $state and offers continuation",async({state,count})=>{
 const partial:FingerprintRecord={...record,state,retained_samples:count,result:{...record.result!,status:"insufficient",decision:"partial",probability:null,used_outputs:count,candidates:[{model:"claude-haiku-4.5",display_name:"Claude Haiku 4.5",probability:null},{model:"gpt-6-astra",display_name:"GPT 6 Astra",probability:null}],samples:[{request_id:"failed",state:"error",message:"请求失败或输出不完整",http_status:429}]}};
 vi.stubGlobal("fetch",vi.fn(async(path:string)=>Response.json(path.endsWith("/model-fingerprints")?{data:[partial],supported_models:FINGERPRINT_MODELS}:{data:[],unavailable_sources:[]})));
 mount(<FingerprintPanel targets={[{account_id:"one",group_id:1,model:"gpt-6-astra"}]}/>);
 await screen.findByText(`有效样本 ${count}/3 · 目标类别 gpt-6-astra/6.1-sol`);
 expect(screen.getByText(new RegExp(`暂定排名 · ${count}/3`))).toBeInTheDocument();
 expect(screen.getByText(/最接近：Claude Haiku 4.5/)).toBeInTheDocument();
 expect(screen.getByText("Claude Haiku 4.5",{selector:"li"})).toBeInTheDocument();
 expect(screen.getByText("GPT 6 Astra",{selector:"li"})).toBeInTheDocument();
 expect(screen.getByText("当前仅显示暂定排名，满 3 个有效样本后计算匹配结果与概率。")).toBeInTheDocument();
 expect(screen.getByText(new RegExp(`已保留 ${count} 个有效样本`))).toBeInTheDocument();
 expect(screen.getByText("请求失败或输出不完整 · HTTP 429")).toBeInTheDocument();
 expect(screen.getByRole("button",{name:state==="running"||state==="queued"?"取消检测":"继续采样"})).toBeEnabled();
 expect(screen.queryByText("12.0%")).not.toBeInTheDocument();
 expect(screen.queryByText("—")).not.toBeInTheDocument();
 expect(screen.queryByText("匹配",{exact:true})).not.toBeInTheDocument();
 expect(screen.queryByText("不匹配",{exact:true})).not.toBeInTheDocument();
});

it("explains why a legacy count cannot be resumed without numeric evidence",async()=>{
 const legacy={...record,result:{...record.result!,used_outputs:1,status:"error",probability:null}};
 vi.stubGlobal("fetch",vi.fn(async(path:string)=>Response.json(path.endsWith("/model-fingerprints")?{data:[legacy],supported_models:FINGERPRINT_MODELS}:{data:[],unavailable_sources:[]})));
 mount(<FingerprintPanel targets={[{account_id:"one",group_id:1,model:"gpt-6-astra"}]}/>);
 await screen.findByText("此旧记录未保留可续采样本，下一次检测将开始可累计的新一轮。");
 expect(screen.getByRole("button",{name:"检测指纹"})).toBeEnabled();
 expect(screen.queryByRole("button",{name:"继续采样"})).not.toBeInTheDocument();
});

it.each(["gpt-6-astra","gemini-3.1-pro","gemini-3.8-flash","gemini-4-argon","gemini-4-carbon"])("channel management filters %s fingerprints independently of quality",async(model)=>{
 const scoped={...record,model,result:{...record.result!,model}};
 const accounts=["one","two","three"].map(id=>({id,name:id,base:`https://${id}.test`,email:"test",state:"idle",message:"",synced_at:1,targets:[{group_id:1,name:`group-${id}`,platform:"openai",channel:id,rate:1,key_id:1,active:true,state:"done",message:"",result:null}]}));
 vi.stubGlobal("fetch",vi.fn(async(input:string)=>Response.json({data:input.endsWith("/accounts")?accounts:input.endsWith("/model-fingerprints")?[scoped,{...scoped,account_id:"two",result:{...scoped.result,status:"match",probability:1}},{...scoped,account_id:"three",result:{...scoped.result,status:"insufficient",decision:"partial",used_outputs:2,probability:null}}]:[],labels:{},unavailable_sources:[]})));
 installManagementPageFixture();mount(<Sub2apiChecks user={`fingerprint-filter-${model}`}/>);await screen.findByText("group-one",{selector:"strong"});
 expect(screen.getByRole("columnheader",{name:/模型指纹/})).toBeInTheDocument();
 const user=userEvent.setup();await user.selectOptions(screen.getByLabelText("检测模型筛选"),model);
 await user.selectOptions(screen.getByLabelText("模型指纹筛选"),"mismatch");
 await waitFor(()=>expect(screen.queryByText("group-two",{selector:"strong"})).not.toBeInTheDocument());
 expect(screen.queryByText("group-three",{selector:"strong"})).not.toBeInTheDocument();
 expect(screen.getByText("group-one",{selector:"strong"})).toBeInTheDocument();expect(screen.getByText("12.0%")).toBeInTheDocument();
 await user.selectOptions(screen.getByLabelText("模型指纹筛选"),"match");
 await waitFor(()=>expect(screen.getByText("group-two",{selector:"strong"})).toBeInTheDocument());
 expect(screen.queryByText("group-three",{selector:"strong"})).not.toBeInTheDocument();
 await user.selectOptions(screen.getByLabelText("模型指纹筛选"),"insufficient");
 await waitFor(()=>expect(screen.getByText("group-three",{selector:"strong"})).toBeInTheDocument());
 expect(screen.queryByText("group-one",{selector:"strong"})).not.toBeInTheDocument();
 expect(screen.queryByText("group-two",{selector:"strong"})).not.toBeInTheDocument();
 await user.selectOptions(screen.getByLabelText("检测模型筛选"),"gpt-6.1-sol");
 await waitFor(()=>expect(screen.queryByText("group-three",{selector:"strong"})).not.toBeInTheDocument());
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

it("waits for backend Gemini support and enables detection after capability refresh",async()=>{
 let updated=false;const writes:unknown[]=[];
 vi.stubGlobal("fetch",vi.fn(async(path:string,init?:RequestInit)=>{
  if(path.endsWith("/channel-management"))return Response.json({data:[],unavailable_sources:[]});
  if(init?.method){writes.push(JSON.parse(init.body as string));return Response.json({accepted:true});}
  return Response.json({data:[],...(updated?{supported_models:FINGERPRINT_MODELS}:{})});
 }));
 mount(<FingerprintPanel targets={[{source_id:"fugue",provider:"native",model:"gemini-4-argon"}]}/>);
 const user=userEvent.setup();
 await screen.findByText("指纹检测服务待更新");
 expect(screen.getByRole("button",{name:"检测指纹"})).toBeDisabled();
 expect(writes).toHaveLength(0);
 const recheck=await screen.findByRole("button",{name:"重新检查"});
 updated=true;await user.click(recheck);
 await waitFor(()=>expect(screen.getByRole("button",{name:"检测指纹"})).toBeEnabled());
 await user.click(screen.getByRole("button",{name:"检测指纹"}));
 expect(writes).toEqual([{targets:[{source_id:"fugue",provider:"native",model:"gemini-4-argon"}]}]);
});

it.each([false,true].flatMap(imported=>["gemini-4-argon","gemini-4-carbon"].map(model=>({imported,model}))))("shares $model site results and cancels the originating job (imported=$imported)",async({imported,model})=>{
 const channel={source_id:"fugue",provider:"native",models:[model],kind:"configured",binding_status:"matched",bound_keys:[{account_id:"one",group_id:1}],probe_fingerprint:"new"} as ManagedChannel;
 const importedChannel={...channel,kind:undefined,account_id:"one",group_id:1};
 if(imported){channel.binding_status=undefined;channel.bound_keys=undefined;}
 let current: FingerprintRecord={...record,model,updated_at_ms:1000,result:{...record.result!,model}};
 const writes:unknown[]=[];
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 vi.stubGlobal("fetch",vi.fn(async(path:string,init?:RequestInit)=>{
  if(path.endsWith("/channel-management"))return Response.json({data:[channel],unavailable_sources:[]});
  if(path.endsWith("/sub2api/channels"))return Response.json({data:imported?[importedChannel]:[],unavailable_sources:[]});
  if(init?.method){writes.push(JSON.parse(init.body as string));current={...current,state:"interrupted",updated_at_ms:3000};return Response.json({accepted:true});}
  return Response.json({data:[current],supported_models:FINGERPRINT_MODELS});
 }));
 render(<QueryClientProvider client={client}><Tooltip.Provider>
  <FingerprintPanel targets={[{source_id:"fugue",provider:"native",model}]}/>
  <FingerprintPanel targets={[{account_id:"one",group_id:1,model}]}/>
 </Tooltip.Provider></QueryClientProvider>);
 await waitFor(()=>expect(screen.getAllByText("12.0%")).toHaveLength(2));
 // A running channel-detail check is also visible in the site/group entry.
 current={...current,account_id:undefined,group_id:undefined,source_id:"fugue",provider:"native",revision:"",state:"running",result:null,updated_at_ms:2000};
 await client.invalidateQueries({queryKey:["model-fingerprints"]});
 await waitFor(()=>expect(screen.getAllByRole("button",{name:"取消检测"})).toHaveLength(2));
 const user=userEvent.setup();await user.click(screen.getAllByRole("button",{name:"取消检测"})[1]);
 expect(writes).toEqual([{targets:[{source_id:"fugue",provider:"native",model}]}]);
 await waitFor(()=>expect(screen.getAllByText("已中断")).toHaveLength(2));
});
