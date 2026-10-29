import { loadSubFilters } from "./sub2apiPreferences";
import { loadView } from "./preferences";
import { readManagementPage } from "./managementQuery";
import {
  managementOwnerHint,
  rememberManagementOwner,
} from "./accountSessionHint";
import type { AccountSession } from "./accountSession";

// Start this navigation's live read alongside authentication and module loading.
// A previous tab-local username selects preferences, never permissions. No
// result is displayed until live authentication and the response owner agree.
function beginManagementRead(owner: string) {
  const filters = loadSubFilters(owner);
  const previous = globalThis.__uniManagementBootstrap;
  if (
    previous &&
    previous.owner === owner &&
    previous.filters === JSON.stringify(filters) &&
    Date.now() - previous.startedAt < 10_000
  )
    return;
  const controller = new AbortController();
  previous?.controller.abort();
  const result = readManagementPage(filters, "", controller.signal, undefined, "mount");
  void result.catch(() => undefined);
  globalThis.__uniManagementBootstrap = {
    owner,
    filters: JSON.stringify(filters),
    startedAt: Date.now(),
    controller,
    result,
  };
}

export function startManagementHintRead() {
  const owner = managementOwnerHint();
  if (owner && loadView(location.origin, true) === "sub2api")
    beginManagementRead(owner);
}

export function startManagementBootstrap(value: unknown) {
  const session = value as Partial<AccountSession> | null;
  if (
    !session?.enabled ||
    !session.authenticated ||
    typeof session.username !== "string" ||
    !session.username
  ) {
    globalThis.__uniManagementBootstrap?.controller.abort();
    delete globalThis.__uniManagementBootstrap;
    rememberManagementOwner({
      enabled: true,
      authenticated: false,
      username: "",
    });
    return;
  }
  rememberManagementOwner(session as AccountSession);
  if (loadView(location.origin, true) === "sub2api")
    beginManagementRead(session.username);
}

startManagementHintRead();
const authentication = globalThis.__uniConsoleSessionBootstrap;
if (authentication)
  void authentication.result
    .then(startManagementBootstrap)
    .catch(() => undefined);
