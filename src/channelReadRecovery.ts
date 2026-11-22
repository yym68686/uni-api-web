import { ApiError } from "./api";
import { RequestTimeoutError } from "./requestDeadline";

// These options are for read-only queries. Do not retry invalid payloads,
// permission denials, or any write whose outcome might be unknown.
export const channelReadRecovery = {
  retry(failureCount: number, error: Error) {
    if (failureCount !== 0) return false;
    const cause = error.cause instanceof Error ? error.cause : error;
    return cause instanceof RequestTimeoutError || cause instanceof TypeError ||
      (cause instanceof ApiError && [408, 502, 503, 504].includes(cause.status));
  },
  retryDelay: 500,
};
