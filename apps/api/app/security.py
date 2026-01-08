from __future__ import annotations

import hashlib
import secrets


def generate_api_key() -> str:
    return f"uai_{secrets.token_urlsafe(32)}"


def key_prefix(full_key: str) -> str:
    if len(full_key) <= 16:
        return full_key
    return f"{full_key[:12]}…{full_key[-4:]}"


def sha256_hex(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()

