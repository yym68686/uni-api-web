// Firefox's chrome namespace is callback-based. Prefer its Promise-based
// browser API; Chrome MV3 also provides Promises through chrome.*.
export function extensionAPI() {
  return globalThis.browser ?? globalThis.chrome;
}
