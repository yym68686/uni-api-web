import type { Stats } from "./types";

type Clocks = Pick<Stats, "successful_response_created" | "successful_first_text">;
export type ChannelLatencyKind = "created" | "text";

export function channelLatencyKind(stats?: Clocks): ChannelLatencyKind {
  return !stats?.successful_response_created?.sample_count && stats?.successful_first_text?.sample_count
    ? "text" : "created";
}

// Missing during a mixed-version rollout does not make the old unscoped
// distribution safe, even when a channel reports a nonzero success count.
export function channelLatency(stats?: Clocks, kind = channelLatencyKind(stats)) {
  return kind === "text" ? stats?.successful_first_text : stats?.successful_response_created;
}
