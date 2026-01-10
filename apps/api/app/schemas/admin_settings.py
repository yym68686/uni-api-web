from __future__ import annotations

from pydantic import BaseModel, Field


class AdminSettingsResponse(BaseModel):
    registration_enabled: bool = Field(alias="registrationEnabled")


class AdminSettingsUpdateRequest(BaseModel):
    registration_enabled: bool | None = Field(default=None, alias="registrationEnabled")

