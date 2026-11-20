import type { Stats } from "./types";

// Missing during a mixed-version rollout does not make the old unscoped
// distribution safe, even when a channel reports a nonzero success count.
export function channelLatency(stats?: Pick<Stats, "successful_response_created">) {
  return stats?.successful_response_created;
}
