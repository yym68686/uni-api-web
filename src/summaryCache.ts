import type { SummaryState } from "./summaryFeed";

const database = "uni-console-summaries-v1";
const ttl = 30 * 60_000;
let epoch = 0;
async function db(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(database, 1);
    let settled = false;
    const timer = setTimeout(() => {
      settled = true;
      reject(Error("cache unavailable"));
    }, 150);
    const fail = () => {
      clearTimeout(timer);
      settled = true;
      reject(Error("cache unavailable"));
    };
    req.onblocked = fail;
    req.onupgradeneeded = () => req.result.createObjectStore("summaries");
    req.onsuccess = () => {
      clearTimeout(timer);
      if (settled) {
        req.result.close();
        return;
      }
      settled = true;
      req.result.onversionchange = () => req.result.close();
      resolve(req.result);
    };
    req.onerror = fail;
  });
}
export async function loadSummaryCache<T>(
  user: string,
  key: string,
): Promise<SummaryState<T> | undefined> {
  if (!user || typeof indexedDB === "undefined") return;
  const started = epoch;
  try {
    const conn = await db();
    return await new Promise((resolve) => {
      const timer = setTimeout(() => {
        conn.close();
        resolve(undefined);
      }, 150);
      const tx = conn.transaction("summaries", "readonly");
      const request = tx
        .objectStore("summaries")
        .get(JSON.stringify([location.origin, user, key]));
      request.onsuccess = () => {
        clearTimeout(timer);
        const value = request.result;
        resolve(
          started === epoch &&
            value &&
            Date.now() - value.at < ttl &&
            typeof value.summary?.version === "string" &&
            Array.isArray(value.summary?.rows) &&
            value.summary.rows.every(
              (row: { id?: unknown; value?: unknown }) =>
                typeof row.id === "string" && row.value,
            )
            ? value.summary
            : undefined,
        );
      };
      request.onerror = () => {
        clearTimeout(timer);
        resolve(undefined);
      };
      tx.oncomplete = () => conn.close();
      tx.onabort = () => {
        clearTimeout(timer);
        conn.close();
        resolve(undefined);
      };
    });
  } catch {
    return undefined;
  }
}
export async function saveSummaryCache<T>(
  user: string,
  key: string,
  summary: SummaryState<T>,
) {
  if (!user || typeof indexedDB === "undefined") return;
  const started = epoch;
  try {
    if (JSON.stringify(summary).length > 8_000_000) return;
    const conn = await db();
    if (started !== epoch) {
      conn.close();
      return;
    }
    const tx = conn.transaction("summaries", "readwrite");
    tx.objectStore("summaries").put(
      { at: Date.now(), summary },
      JSON.stringify([location.origin, user, key]),
    );
    tx.oncomplete = () => conn.close();
    tx.onabort = () => conn.close();
  } catch {
    /* Storage is optional; the live read remains authoritative. */
  }
}
export async function clearSummaryCache() {
  epoch++;
  if (typeof indexedDB === "undefined") return;
  try {
    const conn = await db();
    await new Promise<void>((resolve) => {
      const tx = conn.transaction("summaries", "readwrite");
      tx.objectStore("summaries").clear();
      tx.oncomplete = () => {
        conn.close();
        resolve();
      };
      tx.onabort = () => {
        conn.close();
        resolve();
      };
    });
  } catch {}
}
