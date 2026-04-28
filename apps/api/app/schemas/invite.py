from __future__ import annotations

from pydantic import BaseModel, EmailStr, Field


class InviteVisitRequest(BaseModel):
    invite_code: str | None = Field(default=None, alias="inviteCode")


class InviteeItem(BaseModel):
    id: str
    email: EmailStr
    invited_at: str = Field(alias="invitedAt")
    reward_status: str = Field(alias="rewardStatus")
    reward_usd: float | None = Field(default=None, alias="rewardUsd")


class ReceivedInviteReward(BaseModel):
    id: str
    status: str
    reward_usd: float | None = Field(default=None, alias="rewardUsd")
    created_at: str = Field(alias="createdAt")
    available_at: str | None = Field(default=None, alias="availableAt")
    confirmed_at: str | None = Field(default=None, alias="confirmedAt")


class InviteSummaryResponse(BaseModel):
    invite_code: str = Field(alias="inviteCode")
    invited_total: int = Field(alias="invitedTotal")
    visits_total: int = Field(alias="visitsTotal")
    rewards_pending: int = Field(alias="rewardsPending")
    rewards_confirmed: int = Field(alias="rewardsConfirmed")
    received_reward: ReceivedInviteReward | None = Field(default=None, alias="receivedReward")
    items: list[InviteeItem]
