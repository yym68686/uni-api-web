from __future__ import annotations

import datetime as dt
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator

BrowserAnalyticsEventName = Literal["page_view", "landing_view", "signup_started"]


class AnalyticsCollectRequest(BaseModel):
    model_config = ConfigDict(populate_by_name=True)

    event_id: str | None = Field(default=None, alias="eventId", max_length=120)
    name: BrowserAnalyticsEventName
    anonymous_id: str | None = Field(default=None, alias="anonymousId", max_length=120)
    session_id: str | None = Field(default=None, alias="sessionId", max_length=120)
    timestamp: dt.datetime | None = None
    properties: dict[str, Any] = Field(default_factory=dict)
    context: dict[str, Any] = Field(default_factory=dict)

    @field_validator("event_id", "anonymous_id", "session_id", mode="before")
    @classmethod
    def clean_optional_string(cls, value: object) -> object:
        if not isinstance(value, str):
            return None
        cleaned = value.strip()
        return cleaned or None
