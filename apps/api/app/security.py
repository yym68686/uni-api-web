from __future__ import annotations

import hashlib
import string
import secrets


def generate_api_key() -> str:
    # Keep the OpenAI-style prefix, but restrict the rest to alphanumeric
    # (no "-" / "_" like token_urlsafe()).
    alphabet = string.ascii_letters + string.digits
    body = "".join(secrets.choice(alphabet) for _ in range(43))
    return f"sk-{body}"


def key_prefix(full_key: str) -> str:
    if len(full_key) <= 16:
        return full_key
    return f"{full_key[:12]}…{full_key[-4:]}"


def sha256_hex(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()
