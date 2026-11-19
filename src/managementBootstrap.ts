import { loadSubFilters } from "./sub2apiPreferences";
import { loadView } from "./preferences";
import { readManagementPage } from "./managementQuery";
import { managementBootstrapOverview } from "./managementOverview";
import {
  managementOwnerHint,
  rememberManagementOwner,
} from "./accountSessionHint";
import type { AccountSession } from "./accountSession";
import { preloadManagementWorkspace } from "./workspaceModules";

function managementLocation(): boolean {
  // An explicit workspace in the URL wins over the saved workspace.
  const view = new URLSearchParams(location.hash.slice(1)).get("view");
  return view ? view === "sub2api" : loadView(location.origin, true) === "sub2api";
}

// Start this navigation's live read alongside authentication and module loading.
// A previous tab-local username selects preferences, never permissions. No
// result is displayed until live authentication and the response owner agree.
function beginManagementRead(owner: string) {
  const filters = loadSubFilters(owner);
  const overview = managementBootstrapOverview(filters.model);
  const previous = globalThis.__uniManagementBootstrap;
  if (
    previous &&
    previous.owner === owner &&
    previous.filters === JSON.stringify(filters) &&
    Boolean(previous.overview) === overview &&
    Date.now() - previous.startedAt < 10_000
  )
    return;
  const controller = new AbortController();
  previous?.controller.abort();
  const result = readManagementPage(filters, "", controller.signal, undefined, "mount", false, overview);
  void result.catch(() => undefined);
  globalThis.__uniManagementBootstrap = {
    owner,
    filters: JSON.stringify(filters),
    startedAt: Date.now(),
    controller,
    result,
    overview,
  };
}

export function startManagementHintRead() {
  if (!managementLocation()) return;
  // Static workspace modules contain no account data and do not need to
  // wait for a session. Only a hinted account can start the early API read.
  preloadManagementWorkspace();
  const owner = managementOwnerHint();
  if (owner) beginManagementRead(owner);
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
  if (managementLocation()) {
    beginManagementRead(session.username);
    preloadManagementWorkspace();
  }
}

startManagementHintRead();
const authentication = globalThis.__uniConsoleSessionBootstrap;
if (authentication)
  void authentication.result
    .then(startManagementBootstrap)
    .catch(() => undefined);
