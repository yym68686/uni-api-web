import { makeLimiter } from "./api";

// Group by host, not by source, channel, API path or account. Two sources can
// point at the same site, and its login and API endpoints share capacity.
export function balanceSite(base: string | undefined, fallback: string) {
  try {
    const url = new URL(base || "");
    if (["http:", "https:"].includes(url.protocol)) {
      return `site:${url.hostname.toLowerCase().replace(/\.$/, "")}`;
    }
  } catch { /* Missing site metadata uses a conservative source queue. */ }
  return `unknown:${fallback}`;
}

function keyedLimiter(concurrency: number) {
  const queues = new Map<string, { run: ReturnType<typeof makeLimiter>; users: number }>();
  return async function run<T>(
    key: string,
    job: () => Promise<T>,
    signal: AbortSignal,
  ): Promise<T> {
    let queue = queues.get(key);
    if (!queue) {
      queue = { run: makeLimiter(concurrency), users: 0 };
      queues.set(key, queue);
    }
    queue.users++;
    try {
      return await queue.run(job, signal);
    } finally {
      if (--queue.users === 0) queues.delete(key);
    }
  };
}

export function makeBalanceScheduler() {
  const sites = keyedLimiter(1);
  // Legacy gateways accept only three concurrent balance requests per source.
  // Take this permit AFTER the site queue so waiting channels cannot block
  // other sites. Account queries and separate sources remain independent.
  const gateways = keyedLimiter(3);
  return <T>(site: string, job: () => Promise<T>, signal: AbortSignal, source?: string) =>
    sites(site, () => source ? gateways(source, job, signal) : job(), signal);
}

// Share queues across the balance page and account list, including requests
// still finishing after navigation. Idle queues are removed automatically.
export const scheduleBalance = makeBalanceScheduler();
