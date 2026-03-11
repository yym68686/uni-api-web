from __future__ import annotations

from typing import Protocol

HOP_BY_HOP_HEADERS = {
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailer",
    "transfer-encoding",
    "upgrade",
}

CLIENT_AUTH_HEADERS = {
    "authorization",
    "x-api-key",
}


class _RawHeaders(Protocol):
    raw: list[tuple[bytes, bytes]]


class _RequestLike(Protocol):
    headers: _RawHeaders


def _build_upstream_headers(request: _RequestLike, *, upstream_api_key: str) -> list[tuple[str, str]]:
    headers: list[tuple[str, str]] = []
    for raw_name, raw_value in request.headers.raw:
        try:
            name = raw_name.decode("latin-1")
            value = raw_value.decode("latin-1")
        except Exception:
            continue
        key = name.lower()
        if key in HOP_BY_HOP_HEADERS:
            continue
        if key in {"host", "content-length"} | CLIENT_AUTH_HEADERS:
            continue
        headers.append((name, value))
    headers.append(("authorization", f"Bearer {upstream_api_key}"))
    return headers


def _filter_upstream_response_headers(headers: dict[str, str]) -> dict[str, str]:
    out: dict[str, str] = {}
    for name, value in headers.items():
        key = name.lower()
        if key in HOP_BY_HOP_HEADERS:
            continue
        if key in {"content-length", "content-encoding"}:
            continue
        out[name] = value
    return out
