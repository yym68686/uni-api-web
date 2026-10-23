import { loadSubFilters } from "./sub2apiPreferences";
import { loadView } from "./preferences";
import { readManagementPage } from "./managementQuery";
import type { AccountSession } from "./accountSession";

// Start this navigation's live read while the main UI and lazy management
// component load. Authentication must already have succeeded; no saved result
// is restored, and the component consumes the request only for the same owner
// and exact current filters.
export function startManagementBootstrap(value: unknown) {
  const session = value as Partial<AccountSession> | null;
  if (!session?.enabled || !session.authenticated || typeof session.username !== "string" || !session.username || loadView(location.origin, true) !== "sub2api") return;
  const filters = loadSubFilters(session.username);
  const controller = new AbortController();
  globalThis.__uniManagementBootstrap?.controller.abort();
  const result = readManagementPage(filters, "", controller.signal);
  void result.catch(() => undefined);
  globalThis.__uniManagementBootstrap = { owner: session.username, filters: JSON.stringify(filters), startedAt: Date.now(), controller, result };
}

const authentication = globalThis.__uniConsoleSessionBootstrap;
if (authentication) void authentication.result.then(startManagementBootstrap).catch(() => undefined);
