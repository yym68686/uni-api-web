import "client-only";

let pendingPathname: string | null = null;

export function setPendingPathname(value: string) {
  pendingPathname = value;
}

export function peekPendingPathname() {
  return pendingPathname;
}

export function clearPendingPathname() {
  pendingPathname = null;
}

