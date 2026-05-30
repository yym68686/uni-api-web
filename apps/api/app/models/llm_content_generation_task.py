from __future__ import annotations

import datetime as dt
import uuid

from sqlalchemy import DateTime, ForeignKey, Index, Integer, String
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import Base


class LlmContentGenerationTask(Base):
    __tablename__ = "llm_content_generation_tasks"
    __table_args__ = (
        Index("ix_llm_content_generation_tasks_org_created_at", "org_id", "created_at"),
        Index("ix_llm_content_generation_tasks_user_created_at", "user_id", "created_at"),
        Index("ix_llm_content_generation_tasks_billed_at", "billed_at"),
    )

    upstream_task_id: Mapped[str] = mapped_column(String(128), primary_key=True)
    org_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE"), nullable=False
    )
    user_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("users.id", ondelete="CASCADE"), nullable=False
    )
    api_key_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True), ForeignKey("api_keys.id", ondelete="SET NULL"), nullable=True
    )
    channel_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True), ForeignKey("llm_channels.id", ondelete="SET NULL"), nullable=True
    )
    model_id: Mapped[str] = mapped_column(String(200), nullable=False)
    status: Mapped[str | None] = mapped_column(String(32), nullable=True)
    created_at: Mapped[dt.datetime] = mapped_column(
        DateTime(timezone=True), default=lambda: dt.datetime.now(dt.timezone.utc), nullable=False
    )
    updated_at: Mapped[dt.datetime] = mapped_column(
        DateTime(timezone=True),
        default=lambda: dt.datetime.now(dt.timezone.utc),
        onupdate=lambda: dt.datetime.now(dt.timezone.utc),
        nullable=False,
    )
    billed_at: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    billed_input_tokens: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    billed_cached_tokens: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    billed_output_tokens: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    billed_total_tokens: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    billed_cost_usd_micros: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
