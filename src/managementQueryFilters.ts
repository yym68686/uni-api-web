import type { ManagementRow } from "./channelManagement";
import { UNASSIGNED_ACCOUNT } from "./channelManagement";
import type { SubFilters } from "./sub2apiPreferences";
import type { ModelPrice } from "./types";
import { billingRate, matchesBillingRateFilter } from "./billingRates";
import { matchesPriceFilter } from "./sub2apiPriceCheck";
import { modelMatchStatus, groupQualityResult } from "./sub2apiResults";
import { compactionStatus } from "./SubCompaction";
import { toolUseMatches } from "./toolUse";

export interface ManagementFilterEvidence {
  prices?: ModelPrice[];
  fingerprintReady: boolean;
  fingerprints: (row: ManagementRow) => string[];
  joined: (row: ManagementRow) => string;
}

// Kept independent of React/query caches so server query parity can be checked
// against the exact browser predicates, facet scope and stable ordering.
export function filterManagementRows(
  input: ManagementRow[], filters: SubFilters, evidence: ManagementFilterEvidence,
) {
  return filterManagementCandidates(managementCandidateRows(input,filters,evidence.prices),filters,evidence);
}

export function managementCandidateRows(input:ManagementRow[],filters:SubFilters,prices?:ModelPrice[]) {
  const {search,accountId,model,availability,groupStatus,priceStatus,compaction,toolUse,quality,minQuality,modelMatch}=filters;
  return input.filter(({account,target,checks,selected,accountIds,configured})=>
    (!accountId || (accountId===UNASSIGNED_ACCOUNT ? !accountIds.length : accountIds.includes(accountId))) &&
    (!configured || !account.id || !model || checks.some(c=>c.model===model)) &&
    (groupStatus==="all" || (groupStatus==="unavailable" ? !configured && !target.active : !!configured || target.active)) &&
    `${account.name} ${account.email} ${target.name} ${target.channel} ${target.platform} ${configured?.source_name || ""} ${configured?.members?.map(m=>m.base || "").join(" ") || configured?.base || ""}`.toLowerCase().includes(search.toLowerCase()) &&
    (!availability || (selected ? [selected] : checks).some(check=>availability==="untested" ? !check.result : check.result?.availability.status===availability)) &&
    matchesPriceFilter(priceStatus,selected ? [selected] : checks,prices) &&
    (!modelMatch || (selected ? [selected] : checks).some(check=>modelMatchStatus(check)===modelMatch)) &&
    (!compaction || compactionStatus(target,model)===compaction) && toolUseMatches(target,toolUse,model) &&
    (!quality || groupQualityResult(target)?.verdict===quality) &&
    (minQuality==="" || (!!target.history?.successful && target.history.passed*100>=Number(minQuality)*target.history.successful)));
}

export function filterManagementCandidates(candidates:ManagementRow[],filters:SubFilters,evidence:ManagementFilterEvidence) {
  const {model,maxRate,sort,platform,joined,fingerprint}=filters;
  const billing=new Map(candidates.map(row=>[row,billingRate(row.target,model,row.configured)]));
  const withinRate=(row:ManagementRow)=>matchesBillingRateFilter(billing.get(row)!,maxRate);
  const rates=[...new Set(candidates.filter(({target})=>!platform || target.platform===platform)
    .map(row=>billing.get(row)!.rate).filter((v):v is number=>v!=null && Number.isFinite(v)))];
  if(maxRate && Number.isFinite(Number(maxRate)) && !rates.includes(Number(maxRate))) rates.push(Number(maxRate));
  rates.sort((a,b)=>a-b);
  const platforms=[...new Set(candidates.filter(withinRate).map(({target})=>target.platform).filter(Boolean))].sort();
  const rows=candidates.filter(row=>
    (!fingerprint || !evidence.fingerprintReady || (fingerprint==="not_applicable" ? evidence.fingerprints(row).length===0 : evidence.fingerprints(row).includes(fingerprint))) &&
    withinRate(row) && (!platform || row.target.platform===platform) && (!joined || evidence.joined(row)===joined));
  if(sort)rows.sort((a,b)=>{
    const x=billing.get(a)!.rate,y=billing.get(b)!.rate;
    if(x==null)return y==null?0:1;
    if(y==null)return -1;
    return sort==="asc"?x-y:y-x;
  });
  return {rows,rates,platforms};
}
