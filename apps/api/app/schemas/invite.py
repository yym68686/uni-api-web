from __future__ import annotations

from pydantic import BaseModel, EmailStr, Field


class InviteeItem(BaseModel):
    id: str
    email: EmailStr
    invited_at: str = Field(alias="invitedAt")
    reward_status: str = Field(alias="rewardStatus")
    reward_usd: float | None = Field(default=None, alias="rewardUsd")


class InviteSummaryResponse(BaseModel):
    invite_code: str = Field(alias="inviteCode")
    invited_total: int = Field(alias="invitedTotal")
    rewards_pending: int = Field(alias="rewardsPending")
    rewards_confirmed: int = Field(alias="rewardsConfirmed")
    items: list[InviteeItem]

