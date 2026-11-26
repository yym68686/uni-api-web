import type { ManagedChannel } from "./channelManagement";
import type { FingerprintRecord, FingerprintResult } from "./ModelFingerprints";
import { providerId } from "./format";

const rejectionDetails = {
  group_deleted: [403, "分组已停用", "上游报告采样 Key 所属分组已删除。增加站点余额无效，可在核实后移除该渠道。"],
  group_disabled: [403, "分组已停用", "上游报告采样 Key 所属分组已停用。可手动复检，或在核实后移除该渠道。"],
  group_not_allowed: [403, "分组无权限", "上游不再允许采样 Key 的用户使用此分组。"],
  insufficient_balance: [403, "上游余额不足", "上游因采样 Key 关联的账户余额不足而拒绝请求。"],
  model_not_found: [404, "模型不支持", "上游报告此分组或接口不支持该模型。"],
  upstream_model_unavailable: [503, "上游模型不可用", "上游报告该模型在此分组暂时没有可用渠道。可在上游恢复后手动复检。"],
  route_temporarily_disabled: [503, "渠道临时停用", "网关未派发采样请求，因为匹配渠道被临时停用。"],
  endpoint_gone: [410, "HTTP 410", "采样请求返回 HTTP 410；无法据此确定被撤下的是接口还是分组。"],
  reference_effort_unsupported: [400, "采样参数不支持", "上游 Responses 兼容接口不支持指纹所需的 reasoning.effort=low；本轮已停止，未降低采样要求或切换协议。"],
} as const;
type Rejection = keyof typeof rejectionDetails;
export type GroupRejection = "group_deleted" | "group_disabled";
type Sample = NonNullable<FingerprintResult["samples"]>[number];

function rejectionOf(sample: Sample): Rejection | undefined {
  const code = sample.error_code as Rejection;
  return sample.state === "error" && Object.hasOwn(rejectionDetails, code) &&
    rejectionDetails[code][0] === sample.http_status ? code : undefined;
}

export function fingerprintSampleFailure(sample: Sample) {
  const code = rejectionOf(sample) ?? (sample.http_status === 410 ? "endpoint_gone" : undefined);
  const http = sample.http_status ? `HTTP ${sample.http_status}` : "";
  return code ? { label: rejectionDetails[code][1], detail: http && !rejectionDetails[code][2].includes(http) ? `${rejectionDetails[code][2]} · ${http}` : rejectionDetails[code][2] }
    : { label: sample.http_status === 429 ? "请求被限流" : (sample.http_status || 0) >= 400 ? http : "检测失败",
        detail: [sample.message || "请求失败或输出不完整", http].filter(Boolean).join(" · ") };
}

// A refusal describes the target that received it, not another alias or
// gateway with its own availability. Keep its local fingerprint visible.
export function hasScopedFingerprintRejection(record?: FingerprintRecord) {
  return record?.state === "done" && record.result?.samples?.some(sample => sample.state === "error" &&
    (rejectionOf(sample) || sample.http_status === 410));
}

export function fingerprintFailures(result?: FingerprintResult | null) {
  return (result?.samples || []).filter(sample => sample.state === "error").map(fingerprintSampleFailure);
}

export const groupRejectionDetail = (code: GroupRejection) => rejectionDetails[code][2];

// Only an exact source/provider result for its current single credential can
// describe the whole channel. The shared fingerprint index is for model
// comparisons, not channel state. A later successful sample can clear it.
export function observedGroupRejections(records: FingerprintRecord[], members: Map<string, ManagedChannel>) {
  const evidence = new Map<string, {at: number; updated: number; code?: GroupRejection}>();
  for (const record of records) {
    if (!record.source_id || !record.provider || record.account_id || record.state !== "done" || !record.revision) continue;
    const key = providerId({ source_id: record.source_id, provider: record.provider });
    const member = members.get(key);
    if (member?.credential_count !== 1 || member.probe_fingerprint !== record.revision || !member.models.includes(record.model)) continue;
    const at = (record.result?.checked_at || 0) * 1000;
    const updated = record.updated_at_ms ?? at;
    const previous = evidence.get(key);
    if (previous && (at < previous.at || at === previous.at && updated <= previous.updated)) continue;
    const samples = record.result?.samples || [];
    if (samples.at(-1)?.state === "done") {
      evidence.set(key, {at, updated});
      continue;
    }
    for (const sample of [...samples].reverse()) {
      const code = rejectionOf(sample);
      if (code === "group_deleted" || code === "group_disabled") {
        evidence.set(key, {at, updated, code});
        break;
      }
      // A resumed round prepends saved samples; those aren't proof that this
      // recheck reached the group if every fresh attempt failed.
      if (sample.state === "done") break;
    }
  }
  return new Map([...evidence].flatMap(([key, item]) => item.code ? [[key, item.code] as const] : []));
}
