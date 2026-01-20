from __future__ import annotations

from pydantic import BaseModel, Field


class AdminOverviewKpis(BaseModel):
    calls_24h: int = Field(alias="calls24h")
    errors_24h: int = Field(alias="errors24h")
    spend_usd_24h: float = Field(alias="spendUsd24h")
    active_users_24h: int = Field(alias="activeUsers24h")
    active_keys_24h: int = Field(alias="activeKeys24h")


class AdminOverviewCounts(BaseModel):
    users_total: int = Field(alias="usersTotal")
    users_banned: int = Field(alias="usersBanned")
    channels_total: int = Field(alias="channelsTotal")
    models_config_total: int = Field(alias="modelsConfigTotal")
    models_disabled: int = Field(alias="modelsDisabled")
    announcements_total: int = Field(alias="announcementsTotal")


class AdminOverviewHealthItem(BaseModel):
    id: str
    level: str
    value: int | None = None


class AdminOverviewEventItem(BaseModel):
    id: str
    type: str
    created_at: str = Field(alias="createdAt")
    href: str

    email: str | None = None
    delta_usd: float | None = Field(default=None, alias="deltaUsd")
    balance_usd: float | None = Field(default=None, alias="balanceUsd")

    title: str | None = None
    title_zh: str | None = Field(default=None, alias="titleZh")
    title_en: str | None = Field(default=None, alias="titleEn")

    channel_name: str | None = Field(default=None, alias="channelName")


class AdminOverviewResponse(BaseModel):
    registration_enabled: bool = Field(alias="registrationEnabled")
    kpis: AdminOverviewKpis
    counts: AdminOverviewCounts
    health: list[AdminOverviewHealthItem]
    events: list[AdminOverviewEventItem]
