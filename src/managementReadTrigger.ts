// Diagnostic intent only. Never use these labels for authorization, cache
// keys, freshness, retries or filtering. They describe changes between reads
// started by this mounted hook, not a trusted audit of a physical click.
export type ManagementReadTrigger =
  | "mount"
  | "filter_change"
  | "page_change"
  | "accepted_write"
  | "refetch"
  | "unspecified";

export function managementReadTrigger(value: unknown): ManagementReadTrigger {
  switch (value) {
    case "mount":
    case "filter_change":
    case "page_change":
    case "accepted_write":
    case "refetch":
      return value;
    default:
      return "unspecified";
  }
}

// At most the last started read's inputs live in this hook-local closure.
// They never enter response JSON, logs, persistence or another hook/owner.
export function createManagementReadTriggers() {
  let previous: { owner: string; filters: string; cursor: string } | undefined;
  let writtenOwner: string | undefined;
  return {
    acceptedWrite(owner: string) {
      writtenOwner = owner;
    },
    next(owner: string, filters: string, cursor: string): ManagementReadTrigger {
      const trigger = !previous || previous.owner !== owner
        ? "mount"
        : previous.filters !== filters
          ? "filter_change"
          : previous.cursor !== cursor
            ? "page_change"
            : writtenOwner === owner
              ? "accepted_write"
              : "refetch";
      previous = { owner, filters, cursor };
      writtenOwner = undefined;
      return trigger;
    },
  };
}
