import type { InviteSummaryResponse, ReceivedInviteReward } from "@/lib/types";

export function isReceivedInviteReward(value: unknown): value is ReceivedInviteReward {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  if (typeof v.id !== "string") return false;
  if (typeof v.status !== "string") return false;
  if (typeof v.createdAt !== "string") return false;
  if ("rewardUsd" in v && v.rewardUsd !== null && typeof v.rewardUsd !== "number") return false;
  if ("availableAt" in v && v.availableAt !== null && typeof v.availableAt !== "string") return false;
  if ("confirmedAt" in v && v.confirmedAt !== null && typeof v.confirmedAt !== "string") return false;
  return true;
}

export function isInviteSummaryResponse(value: unknown): value is InviteSummaryResponse {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  if (typeof v.inviteCode !== "string") return false;
  if (typeof v.invitedTotal !== "number") return false;
  if (typeof v.visitsTotal !== "number") return false;
  if (typeof v.rewardsPending !== "number") return false;
  if (typeof v.rewardsConfirmed !== "number") return false;
  if ("receivedReward" in v && v.receivedReward !== null && !isReceivedInviteReward(v.receivedReward)) return false;
  if (!Array.isArray(v.items)) return false;
  return true;
}
