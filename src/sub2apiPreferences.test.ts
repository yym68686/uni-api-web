import { expect, it } from "vitest";
import { loadSubFilters, saveSubFilters, subFilterDefaults } from "./sub2apiPreferences";
it("defaults to ascending multiplier order while preserving explicit descending order",()=>{
 expect(loadSubFilters("new-default")).toMatchObject({sort:"asc",groupStatus:"all"});
 localStorage.setItem("uni-console-sub2api-filters:v1:missing-order",JSON.stringify({model:"gpt-5.5"}));
 expect(loadSubFilters("missing-order")).toMatchObject({model:"gpt-5.5",sort:"asc"});
 saveSubFilters("saved-order",{...subFilterDefaults,sort:"desc"});expect(loadSubFilters("saved-order").sort).toBe("desc");
 localStorage.setItem("uni-console-sub2api-filters:v1:saved-order",JSON.stringify({sort:""}));
 expect(loadSubFilters("saved-order").sort).toBe("asc");
});
it("preserves name exclusions per user and defaults older or invalid preferences to empty",()=>{
 localStorage.setItem("uni-console-sub2api-filters:v1:older-exclusion",JSON.stringify({search:"site"}));
 expect(loadSubFilters("older-exclusion")).toMatchObject({search:"site",excludeName:""});
 localStorage.setItem("uni-console-sub2api-filters:v1:invalid-exclusion",JSON.stringify({excludeName:42}));
 expect(loadSubFilters("invalid-exclusion").excludeName).toBe("");
 saveSubFilters("saved-exclusion",{...subFilterDefaults,excludeName:"KiRo"});
 expect(loadSubFilters("saved-exclusion").excludeName).toBe("KiRo");
 expect(loadSubFilters("other-exclusion").excludeName).toBe("");
 saveSubFilters("saved-exclusion",{...subFilterDefaults});
 expect(loadSubFilters("saved-exclusion").excludeName).toBe("");
});
