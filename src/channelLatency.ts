import type { Stats } from "./types";

// Keep the existing Responses clock when present. Native protocol samples are
// separately labelled; legacy first_output must never become response.created.
export function channelLatency(stats?: Pick<Stats, "response_created" | "first_output">) {
  const created = stats?.response_created;
  return created?.sample_count || created?.p50_ms != null
    ? { value: created, kind: "created" as const }
    : { value: stats?.first_output, kind: "output" as const };
}
export function latencyLabel(kind: "created" | "output") {
  return kind === "created" ? "首个 response.created" : "首个上游输出（含正文、推理或工具调用）";
}
