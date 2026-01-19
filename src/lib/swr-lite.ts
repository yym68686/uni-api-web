import "client-only";

import * as React from "react";

type Key = string;

interface CacheEntry<T> {
  data: T | undefined;
  error: unknown;
  promise: Promise<T> | null;
  fetchedAt: number;
  version: number;
  fetcher: ((key: Key) => Promise<T>) | null;
  listeners: Set<() => void>;
}

const store = new Map<Key, CacheEntry<unknown>>();

function getEntry<T>(key: Key): CacheEntry<T> {
  const existing = store.get(key) as CacheEntry<T> | undefined;
  if (existing) return existing;

  const created: CacheEntry<T> = {
    data: undefined,
    error: undefined,
    promise: null,
    fetchedAt: 0,
    version: 0,
    fetcher: null,
    listeners: new Set()
  };
  store.set(key, created as CacheEntry<unknown>);
  return created;
}

export function peekSwrLite<T>(key: Key): T | undefined {
  const existing = store.get(key) as CacheEntry<T> | undefined;
  return existing?.data;
}

function notify(entry: CacheEntry<unknown>) {
  entry.version += 1;
  for (const listener of entry.listeners) listener();
}

const focusRegistry = new Map<Key, { count: number; revalidate: () => void }>();
let focusListenerAttached = false;

function handleFocus() {
  for (const { revalidate } of focusRegistry.values()) {
    revalidate();
  }
}

function ensureFocusListener() {
  if (focusListenerAttached) return;
  focusListenerAttached = true;

  window.addEventListener("focus", handleFocus, { passive: true });
  document.addEventListener(
    "visibilitychange",
    () => {
      if (document.visibilityState === "visible") handleFocus();
    },
    { passive: true }
  );
}

function registerFocusKey(key: Key, revalidate: () => void) {
  const existing = focusRegistry.get(key);
  if (existing) {
    existing.count += 1;
    existing.revalidate = revalidate;
  } else {
    focusRegistry.set(key, { count: 1, revalidate });
    ensureFocusListener();
  }
}

function unregisterFocusKey(key: Key) {
  const existing = focusRegistry.get(key);
  if (!existing) return;
  existing.count -= 1;
  if (existing.count <= 0) focusRegistry.delete(key);
}

export interface SwrLiteOptions<T> {
  fallbackData?: T;
  dedupingIntervalMs?: number;
  revalidateOnFocus?: boolean;
}

export interface SwrLiteResponse<T> {
  data: T | undefined;
  error: unknown;
  isValidating: boolean;
  mutate: (
    data?:
      | T
      | Promise<T>
      | ((current: T | undefined) => T | Promise<T>),
    opts?: { revalidate?: boolean }
  ) => Promise<T | undefined>;
}

async function revalidateEntry<T>(
  key: Key,
  entry: CacheEntry<T>,
  dedupingIntervalMs: number
): Promise<T> {
  const now = Date.now();
  const age = now - entry.fetchedAt;

  if (entry.promise) return entry.promise;
  if (entry.data !== undefined && age >= 0 && age < dedupingIntervalMs) {
    return entry.data;
  }
  if (!entry.fetcher) {
    throw new Error(`No fetcher registered for key: ${key}`);
  }

  entry.error = undefined;
  const promise = entry.fetcher(key);
  entry.promise = promise;
  notify(entry as CacheEntry<unknown>);

  try {
    const data = await promise;
    entry.data = data;
    entry.fetchedAt = Date.now();
    entry.promise = null;
    notify(entry as CacheEntry<unknown>);
    return data;
  } catch (err) {
    entry.error = err;
    entry.promise = null;
    notify(entry as CacheEntry<unknown>);
    throw err;
  }
}

export function useSwrLite<T>(
  key: Key | null,
  fetcher: (key: Key) => Promise<T>,
  options: SwrLiteOptions<T> = {}
): SwrLiteResponse<T> {
  const { fallbackData, dedupingIntervalMs = 2000, revalidateOnFocus = false } = options;

  const stableFetcher = React.useCallback((nextKey: Key) => fetcher(nextKey), [fetcher]);

  const version = React.useSyncExternalStore(
    React.useCallback(
      (onStoreChange) => {
        if (!key) return () => {};
        const entry = getEntry<T>(key);
        entry.listeners.add(onStoreChange);
        return () => entry.listeners.delete(onStoreChange);
      },
      [key]
    ),
    React.useCallback(() => {
      if (!key) return 0;
      return getEntry<T>(key).version;
    }, [key]),
    React.useCallback(() => 0, [])
  );

  const entry = key ? getEntry<T>(key) : null;
  if (entry) {
    entry.fetcher = stableFetcher;
    if (entry.data === undefined && fallbackData !== undefined) {
      entry.data = fallbackData;
    }
  }

  const revalidate = React.useCallback(async () => {
    if (!key) return undefined;
    const e = getEntry<T>(key);
    e.fetcher = stableFetcher;
    try {
      return await revalidateEntry(key, e, dedupingIntervalMs);
    } catch {
      return undefined;
    }
  }, [dedupingIntervalMs, key, stableFetcher]);

  React.useEffect(() => {
    if (!key) return;
    if (!revalidateOnFocus) return;
    registerFocusKey(key, () => void revalidate());
    return () => unregisterFocusKey(key);
  }, [key, revalidate, revalidateOnFocus]);

  const mutate = React.useCallback<SwrLiteResponse<T>["mutate"]>(
    async (nextData, opts) => {
      if (!key) return undefined;
      const e = getEntry<T>(key);
      e.fetcher = stableFetcher;

      if (typeof nextData === "function") {
        const updater = nextData as (current: T | undefined) => T | Promise<T>;
        const computed = updater(e.data);
        e.data = await computed;
      } else if (nextData !== undefined) {
        e.data = await nextData;
      }
      e.error = undefined;
      e.fetchedAt = Date.now();
      notify(e as CacheEntry<unknown>);

      if (opts?.revalidate) {
        return await revalidateEntry(key, e, dedupingIntervalMs).catch(() => undefined);
      }
      return e.data;
    },
    [dedupingIntervalMs, key, stableFetcher]
  );

  void version;

  return {
    data: entry?.data,
    error: entry?.error,
    isValidating: Boolean(entry?.promise),
    mutate
  };
}

export async function mutateSwrLite<T>(
  key: Key,
  nextData?: T | Promise<T> | ((current: T | undefined) => T | Promise<T>),
  opts?: { revalidate?: boolean; dedupingIntervalMs?: number }
): Promise<T | undefined> {
  const entry = getEntry<T>(key);
  const dedupingIntervalMs = opts?.dedupingIntervalMs ?? 2000;

  if (typeof nextData === "function") {
    const updater = nextData as (current: T | undefined) => T | Promise<T>;
    const computed = updater(entry.data);
    entry.data = await computed;
  } else if (nextData !== undefined) {
    entry.data = await nextData;
  }
  entry.error = undefined;
  entry.fetchedAt = Date.now();
  notify(entry as CacheEntry<unknown>);

  if (opts?.revalidate) {
    return await revalidateEntry(key, entry, dedupingIntervalMs).catch(() => undefined);
  }

  return entry.data;
}
