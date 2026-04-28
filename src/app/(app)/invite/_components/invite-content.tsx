import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { isInviteSummaryResponse } from "@/lib/invite-summary";
import type { InviteSummaryResponse } from "@/lib/types";
import { InviteContentClient } from "./invite-content-client";

export async function getInviteSummary() {
  const res = await fetch(buildBackendUrl("/invite/summary"), {
    cache: "no-store",
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
