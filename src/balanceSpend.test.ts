import { expect, it } from "vitest";
import { balanceSpend } from "./balanceSpend";
import type { SubChannelSpendResult } from "./SubChannelSpend";
const complete=(amount:number):SubChannelSpendResult=>({isPending:false,isError:false,data:{scope:"matched_requests",from:1,to:86401,status:"complete",actual_cost_usd:amount}});
const noRecords:SubChannelSpendResult={isPending:false,isError:false,data:{scope:"matched_requests",from:1,to:86401,status:"no_records",actual_cost_usd:null}};
it("preserves 24h totals when another configured model has no requests",()=>{
 expect(balanceSpend([complete(3),noRecords,complete(2)])).toMatchObject({actual:5,upperBound:false});
 expect(balanceSpend([noRecords])).toMatchObject({actual:null,label:"—"});
 expect(balanceSpend([complete(0)])).toMatchObject({actual:0,upperBound:false});
});
it("retains reconciled costs as a bound when another model remains unknown",()=>{
 const partial:SubChannelSpendResult={isPending:false,isError:false,data:{scope:"matched_requests",from:1,to:86401,status:"unmatched",actual_cost_usd:null,matched_cost_usd:1.5,matched_attempts:2,total_attempts:3}};
 expect(balanceSpend([complete(3),partial,noRecords])).toMatchObject({actual:4.5,upperBound:true});
 expect(balanceSpend([complete(3),{data:undefined,isPending:false,isError:true}])).toMatchObject({actual:3,upperBound:true,label:"查询失败"});
 expect(balanceSpend([{data:undefined,isPending:false,isError:true}])).toMatchObject({actual:null,label:"查询失败"});
 expect(balanceSpend([{data:undefined,isPending:true,isError:false}])).toMatchObject({actual:null,label:"核对中"});
});
