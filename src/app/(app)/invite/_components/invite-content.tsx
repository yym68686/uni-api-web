import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { CACHE_TAGS } from "@/lib/cache-tags";
import type { InviteSummaryResponse } from "@/lib/types";
import { InviteContentClient } from "./invite-content-client";

function isInviteSummaryResponse(value: unknown): value is InviteSummaryResponse {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  if (typeof v.inviteCode !== "string") return false;
  if (typeof v.invitedTotal !== "number") return false;
  if (typeof v.visitsTotal !== "number") return false;
  if (typeof v.rewardsPending !== "number") return false;
  if (typeof v.rewardsConfirmed !== "number") return false;
  if (!Array.isArray(v.items)) return false;
  return true;
}

export async function getInviteSummary() {
  const res = await fetch(buildBackendUrl("/invite/summary"), {
    cache: "force-cache",
    next: { tags: [CACHE_TAGS.inviteSummary], revalidate: 30 },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json().catch(() => null);
  if (!isInviteSummaryResponse(json)) return null;
  return json;
}

interface InviteContentProps {
  initialSummary: InviteSummaryResponse | null;
}

export function InviteContent({ initialSummary }: InviteContentProps) {
  return <InviteContentClient initialSummary={initialSummary} />;
}
