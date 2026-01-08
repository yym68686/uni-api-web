from __future__ import annotations

from pydantic import BaseModel


class AccountDeleteResponse(BaseModel):
    ok: bool
    id: str

