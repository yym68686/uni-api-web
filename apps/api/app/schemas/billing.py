from __future__ import annotations

from pydantic import BaseModel, Field


class BillingLedgerItem(BaseModel):
    id: str
    type: str
    delta_usd: float = Field(alias="deltaUsd")
    balance_usd: float = Field(alias="balanceUsd")
    created_at: str = Field(alias="createdAt")


class BillingLedgerListResponse(BaseModel):
    items: list[BillingLedgerItem]

