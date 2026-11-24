import { expect, it } from "vitest";
import { loadSubFilters, saveSubFilters, subFilterDefaults } from "./sub2apiPreferences";
it("defaults to ascending multiplier order while preserving explicit descending order",()=>{
 expect(loadSubFilters("new-default")).toMatchObject({sort:"asc",groupStatus:"available"});
 localStorage.setItem("uni-console-sub2api-filters:v1:missing-order",JSON.stringify({model:"gpt-5.5"}));
 expect(loadSubFilters("missing-order")).toMatchObject({model:"gpt-5.5",sort:"asc"});
 saveSubFilters("saved-order",{...subFilterDefaults,sort:"desc"});expect(loadSubFilters("saved-order").sort).toBe("desc");
 localStorage.setItem("uni-console-sub2api-filters:v1:saved-order",JSON.stringify({sort:""}));
 expect(loadSubFilters("saved-order").sort).toBe("asc");
});
it("migrates the old all-groups default without losing filters and remembers new choices", () => {
  for (const groupStatus of ["all", "unavailable"]) {
    const user = `legacy-${groupStatus}`;
    localStorage.setItem(
      `uni-console-sub2api-filters:v1:${user}`,
      JSON.stringify({ groupStatus, accountId: "site", model: "gpt-5.5" }),
    );
    const filters = loadSubFilters(user);
    expect(filters).toMatchObject({
      groupStatus: groupStatus === "all" ? "available" : groupStatus,
      accountId: "site",
      model: "gpt-5.5",
    });
    saveSubFilters(user, { ...filters, groupStatus: "all" });
    expect(loadSubFilters(user)).toMatchObject({ groupStatus: "all", accountId: "site" });
  }
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
it("persists each tier filter and discards invalid or reset values", () => {
  const user = "saved-service-tiers";
  localStorage.setItem(`uni-console-sub2api-filters:v1:${user}`, JSON.stringify({ fast: "supported", ultrafast: "invalid" }));
  expect(loadSubFilters(user).fast).toBe("supported");
  expect(loadSubFilters(user).ultrafast).toBeUndefined();
  saveSubFilters(user, { ...subFilterDefaults, fast: "not_honored", ultrafast: "unknown" });
  expect(loadSubFilters(user)).toMatchObject({ fast: "not_honored", ultrafast: "unknown" });
  saveSubFilters(user, subFilterDefaults);
  expect(loadSubFilters(user)).toEqual(subFilterDefaults);
});
