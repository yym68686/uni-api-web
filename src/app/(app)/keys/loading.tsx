"use client";

import { ApiKeysPageClient } from "@/components/keys/api-keys-page-client";
import { API_PATHS } from "@/lib/api-paths";
import { peekSwrLite } from "@/lib/swr-lite";
import type { ApiKeyItem } from "@/lib/types";

import { KeysPageSkeleton } from "./_components/keys-skeleton";

export default function Loading() {
  const cached = peekSwrLite<ApiKeyItem[]>(API_PATHS.keys);
  if (cached === undefined) return <KeysPageSkeleton />;
  return <ApiKeysPageClient initialItems={cached} autoRevalidate={false} />;
}

