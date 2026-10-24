import { expect, it } from "vitest";
import { loadSubFilters, saveSubFilters, subFilterDefaults } from "./sub2apiPreferences";
it("defaults and resets to ascending multipliers while preserving explicit saved order",()=>{
 expect(loadSubFilters("new-default").sort).toBe("asc");
 localStorage.setItem("uni-console-sub2api-filters:v1:missing-order",JSON.stringify({model:"gpt-5.5"}));
 expect(loadSubFilters("missing-order")).toMatchObject({model:"gpt-5.5",sort:"asc"});
 for(const sort of ["desc",""]){saveSubFilters("saved-order",{...subFilterDefaults,sort});expect(loadSubFilters("saved-order").sort).toBe(sort);}
 saveSubFilters("saved-order",{...subFilterDefaults});expect(loadSubFilters("saved-order").sort).toBe("asc");
});
