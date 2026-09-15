import { expect, it } from "vitest";
import { catalogMetrics, emptyStats, ranges } from "./analytics";
import type { Catalog, Metrics } from "./types";
it("joins persisted statistics using channel identity while preserving key order and zero-sample routes", () => {
 const data = ["third","first","unused"].map(provider=>({provider,model:"alias",upstream_model:"upstream",endpoint:"all",stream:null,stats:emptyStats()}));
 const catalog = {data,snapshot_revision:"current"} as Catalog;
 const metrics = {data:[{...data[1],stats:{...emptyStats(),success:7,success_rate_denominator:7}},{...data[0],stats:{...emptyStats(),failed:4}}]} as Metrics;
 expect(catalogMetrics(catalog,metrics).map(row=>[row.provider,row.stats.success,row.stats.failed])).toEqual([["third",0,4],["first",7,0],["unused",0,0]]);
 expect(catalogMetrics(catalog,undefined)).toEqual([]);
});
it("calendar choices use calendar ranges rather than rolling durations",()=>{
 expect(Object.fromEntries(ranges).week).toBe("本周");expect(Object.fromEntries(ranges).month).toBe("本月");expect(Object.fromEntries(ranges).today).toBe("今天");
});
