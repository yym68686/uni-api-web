from __future__ import annotations

import datetime as dt
import uuid

from app.schemas.keys import ApiKeyCreateRequest, ApiKeyCreateResponse, ApiKeyItem, ApiKeysListResponse
from app.security import generate_api_key, key_prefix


class KeyStore:
    def __init__(self) -> None:
        now = dt.datetime.now(dt.timezone.utc)
        self._items: list[tuple[str, ApiKeyItem]] = []

        for name, created_days_ago in [("prod-default", 12), ("staging", 3)]:
            full_key = generate_api_key()
            item = ApiKeyItem(
                id=str(uuid.uuid4()),
                name=name,
                prefix=key_prefix(full_key),
                createdAt=(now - dt.timedelta(days=created_days_ago)).isoformat(),
                lastUsedAt=(now - dt.timedelta(hours=2)).isoformat(),
                revokedAt=None,
            )
            self._items.append((full_key, item))

    def list(self) -> ApiKeysListResponse:
        items = [item for _full, item in self._items]
        items.sort(key=lambda i: i.created_at, reverse=True)
        return ApiKeysListResponse(items=items)

    def create(self, input: ApiKeyCreateRequest) -> ApiKeyCreateResponse:
        full_key = generate_api_key()
        item = ApiKeyItem(
            id=str(uuid.uuid4()),
            name=input.name.strip(),
            prefix=key_prefix(full_key),
            createdAt=dt.datetime.now(dt.timezone.utc).isoformat(),
        )
        self._items.insert(0, (full_key, item))
        return ApiKeyCreateResponse(item=item, key=full_key)

    def revoke(self, id_: str) -> ApiKeyItem | None:
        now = dt.datetime.now(dt.timezone.utc).isoformat()
        for idx, (full, item) in enumerate(self._items):
            if item.id == id_:
                updated = item.model_copy(update={"revokedAt": now})
                self._items[idx] = (full, updated)
                return updated
        return None


key_store = KeyStore()
