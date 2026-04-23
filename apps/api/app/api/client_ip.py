from __future__ import annotations

from fastapi import Request

CLIENT_IP_HEADER_CANDIDATES = (
    "cf-connecting-ip",
    "true-client-ip",
    "x-forwarded-for",
    "x-real-ip",
)


def _normalize_ip_candidate(value: str | None) -> str | None:
    if not isinstance(value, str):
        return None
    candidate = value.split(",")[0].strip()
    if candidate == "":
        return None
    return candidate[:64]


def extract_request_client_ip(request: Request) -> str | None:
    for header_name in CLIENT_IP_HEADER_CANDIDATES:
        candidate = _normalize_ip_candidate(request.headers.get(header_name))
        if candidate:
            return candidate
    if request.client and request.client.host:
        return _normalize_ip_candidate(request.client.host)
    return None


def extract_request_client_ip_or_localhost(request: Request) -> str:
    return extract_request_client_ip(request) or "127.0.0.1"
