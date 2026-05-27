from __future__ import annotations

from pydantic import BaseModel, Field


class AdminSettingsResponse(BaseModel):
    registration_enabled: bool = Field(alias="registrationEnabled")
    billing_topup_enabled: bool = Field(alias="billingTopupEnabled")
    billing_payment_card_enabled: bool = Field(alias="billingPaymentCardEnabled")
    billing_payment_alipay_enabled: bool = Field(alias="billingPaymentAlipayEnabled")
    billing_payment_wxpay_enabled: bool = Field(alias="billingPaymentWxpayEnabled")
    new_user_trial_enabled: bool = Field(alias="newUserTrialEnabled")
    new_user_trial_balance: float = Field(alias="newUserTrialBalance")


class AdminSettingsUpdateRequest(BaseModel):
    registration_enabled: bool | None = Field(default=None, alias="registrationEnabled")
    billing_topup_enabled: bool | None = Field(default=None, alias="billingTopupEnabled")
    billing_payment_card_enabled: bool | None = Field(default=None, alias="billingPaymentCardEnabled")
    billing_payment_alipay_enabled: bool | None = Field(default=None, alias="billingPaymentAlipayEnabled")
    billing_payment_wxpay_enabled: bool | None = Field(default=None, alias="billingPaymentWxpayEnabled")
    new_user_trial_enabled: bool | None = Field(default=None, alias="newUserTrialEnabled")
    new_user_trial_balance: float | None = Field(
        default=None,
        alias="newUserTrialBalance",
        ge=0,
        le=1_000_000,
    )
