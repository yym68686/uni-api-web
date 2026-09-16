import { cleanBase } from "./api";
import type { Connection } from "./types";

const storageKey = "uni-console-connection:v1";

export function loadConnection(): Connection | null {
  try {
    const saved = JSON.parse(sessionStorage.getItem(storageKey) || "null");
    if (
      saved &&
      typeof saved.base === "string" &&
      typeof saved.key === "string" &&
      (saved.key.trim() || typeof saved.sourceId === "string")
    )
      return {
        base: cleanBase(saved.base),
        key: saved.key.trim(),
        sourceId: typeof saved.sourceId === "string" ? saved.sourceId : undefined,
        session: crypto.randomUUID(),
      };
  } catch {
    // Invalid or unavailable session storage leaves the login form accessible.
  }
  return null;
}

export function saveConnection(connection: Connection) {
  try {
    sessionStorage.setItem(
      storageKey,
      JSON.stringify({ base: connection.base, key: connection.key, sourceId: connection.sourceId }),
    );
  } catch {
    // The connection still works for the current page lifetime.
  }
}

export function clearConnection() {
  try {
    sessionStorage.removeItem(storageKey);
  } catch {
    // Ignore unavailable storage.
  }
}
