// This tab-local name selects saved filter preferences only. It never proves
// authentication: the server checks the session cookie on every read, and the
// UI waits for the current authentication response before consuming any page.
const key = "uni-console-management-owner-hint:v1";
export function managementOwnerHint(): string {
  try {
    return sessionStorage.getItem(key) || "";
  } catch {
    return "";
  }
}
export function rememberManagementOwner(value: {
  enabled: boolean;
  authenticated: boolean;
  username: string;
}) {
  try {
    if (value.enabled && value.authenticated && value.username)
      sessionStorage.setItem(key, value.username);
    else sessionStorage.removeItem(key);
  } catch {
    /* Storage is optional; a fresh authenticated read still works. */
  }
}
