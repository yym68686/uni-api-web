import crypto from "node:crypto";

import type {
  ApiKeyCreateRequest,
  ApiKeyCreateResponse,
  ApiKeyItem,
  ApiKeysListResponse
} from "@/lib/types";

interface ApiKeyRecord extends ApiKeyItem {
  key: string;
}

interface KeyStore {
  items: ApiKeyRecord[];
}

declare global {
  var __uniApiKeyStore: KeyStore | undefined;
}

function base64Url(bytes: Uint8Array) {
  return Buffer.from(bytes)
    .toString("base64")
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
}

function buildPrefix(fullKey: string) {
  return `${fullKey.slice(0, 12)}…${fullKey.slice(-4)}`;
}

function getStore(): KeyStore {
  if (!globalThis.__uniApiKeyStore) {
    const now = new Date();
    globalThis.__uniApiKeyStore = {
      items: [
        {
          id: crypto.randomUUID(),
          name: "prod-default",
          key: `sk-${base64Url(crypto.randomBytes(24))}`,
          prefix: "",
          createdAt: new Date(now.getTime() - 1000 * 60 * 60 * 24 * 12).toISOString(),
          lastUsedAt: new Date(now.getTime() - 1000 * 60 * 6).toISOString(),
          spendUsd: 3.2
        },
        {
          id: crypto.randomUUID(),
          name: "staging",
          key: `sk-${base64Url(crypto.randomBytes(24))}`,
          prefix: "",
          createdAt: new Date(now.getTime() - 1000 * 60 * 60 * 24 * 3).toISOString(),
          lastUsedAt: new Date(now.getTime() - 1000 * 60 * 60 * 8).toISOString(),
          spendUsd: 0.4
        }
      ]
    };
    globalThis.__uniApiKeyStore.items = globalThis.__uniApiKeyStore.items.map(
      (item) => ({ ...item, prefix: buildPrefix(item.key) })
    );
  }
  return globalThis.__uniApiKeyStore!;
}

export function listApiKeys(): ApiKeysListResponse {
  const store = getStore();
  const items: ApiKeyItem[] = store.items
    .toSorted((a, b) => b.createdAt.localeCompare(a.createdAt))
    .map(({ key: _key, ...rest }) => rest);
  return { items };
}

export function createApiKey(input: ApiKeyCreateRequest): ApiKeyCreateResponse {
  const store = getStore();
  const key = `sk-${base64Url(crypto.randomBytes(32))}`;
  const item: ApiKeyRecord = {
    id: crypto.randomUUID(),
    name: input.name,
    key,
    prefix: buildPrefix(key),
    createdAt: new Date().toISOString(),
    spendUsd: 0
  };
  store.items.unshift(item);
  const { key: _key, ...publicItem } = item;
  return { item: publicItem, key };
}

export function revokeApiKey(id: string): ApiKeyItem | null {
  const store = getStore();
  const found = store.items.find((k) => k.id === id);
  if (!found) return null;
  if (!found.revokedAt) found.revokedAt = new Date().toISOString();
  const { key: _key, ...publicItem } = found;
  return publicItem;
}
