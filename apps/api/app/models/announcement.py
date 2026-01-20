from __future__ import annotations

import datetime as dt
import uuid

from sqlalchemy import DateTime, String
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import Base


class Announcement(Base):
    __tablename__ = "announcements"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    title: Mapped[str] = mapped_column(String(180), nullable=False)
    title_zh: Mapped[str | None] = mapped_column(String(180), nullable=True)
    title_en: Mapped[str | None] = mapped_column(String(180), nullable=True)
    content: Mapped[str] = mapped_column(String(2000), nullable=False)
    content_zh: Mapped[str | None] = mapped_column(String(2000), nullable=True)
    content_en: Mapped[str | None] = mapped_column(String(2000), nullable=True)
    level: Mapped[str] = mapped_column(String(16), nullable=False, default="warning")

    created_at: Mapped[dt.datetime] = mapped_column(
        DateTime(timezone=True), default=lambda: dt.datetime.now(dt.timezone.utc), nullable=False
    )
