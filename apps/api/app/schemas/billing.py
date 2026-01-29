from __future__ import annotations

from pydantic import BaseModel, Field, StrictInt


class BillingLedgerItem(BaseModel):
    id: str
    type: str
    delta_usd: float = Field(alias="deltaUsd")
    balance_usd: float = Field(alias="balanceUsd")
    created_at: str = Field(alias="createdAt")


class BillingLedgerListResponse(BaseModel):
    items: list[BillingLedgerItem]


class BillingSettingsResponse(BaseModel):
    billing_topup_enabled: bool = Field(alias="billingTopupEnabled")


class BillingTopupCheckoutRequest(BaseModel):
    amount_usd: StrictInt = Field(alias="amountUsd", ge=5, le=5000)


class BillingTopupCheckoutResponse(BaseModel):
    checkout_url: str = Field(alias="checkoutUrl")
    request_id: str = Field(alias="requestId")


class BillingTopupStatusResponse(BaseModel):
    request_id: str = Field(alias="requestId")
    status: str
    units: int
    new_balance: float | None = Field(default=None, alias="newBalance")
