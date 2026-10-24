import { expect, it } from "vitest";
import { modelIsAvailable } from "./ChannelModelSelection";
import { importModelLabel, modelIsDefaultSelected, availabilityCounts } from "./sub2apiResults";
import type { SubModelCheck } from "./sub2apiResults";
import { buildOptimizationPlan } from "./channelOptimizationPlan";
import { optimizationFixture, modelCheck } from "./channelOptimization.test-data";

it.each(["missing", "missing_output"] as const)("keeps %s responses selectable and out of automatic additions", terminal_status => {
 const check = {model:"new",state:"done",result:{availability:{status:"success",terminal_status}}} as SubModelCheck;
 expect(modelIsAvailable(check)).toBe(true);
 expect(modelIsDefaultSelected(check)).toBe(false);
 expect(importModelLabel(check)).toMatch(/^成功，但/);
 expect(availabilityCounts([check])).toMatchObject({success:1,failed:0,warning:1});
 const s=optimizationFixture();
 s.accounts[0].targets[0].models=[modelCheck("old"),{...modelCheck("new"),result:{...modelCheck("new").result!,availability:{...modelCheck("new").result!.availability,terminal_status}}}];
 expect(buildOptimizationPlan(s,["add"]).changes).toEqual([]);
});

it("does not count queued reruns as old failures or untested models",()=>{
 const make=(state:string,status?:string)=>({model:state,state,message:"",result:status?{availability:{status}}:null}) as SubModelCheck;
 expect(availabilityCounts([make("queued","error"),make("running"),make("interrupted","success"),make("done","error"),make("done","success"),make("idle"),make("error")])).toEqual({success:1,warning:0,failed:2,untested:1,pending:2,interrupted:1});
});
