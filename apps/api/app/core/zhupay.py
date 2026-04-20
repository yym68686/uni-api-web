from __future__ import annotations

import base64
import time
from dataclasses import dataclass
from decimal import Decimal, InvalidOperation, ROUND_HALF_UP
from functools import lru_cache
from typing import Mapping

import httpx
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding

from app.core.config import settings


SIGN_TYPE = "RSA"
SUPPORTED_PAYMENT_METHODS = {"alipay", "wxpay"}


class ZhupayError(RuntimeError):
    pass


@dataclass(frozen=True)
class ZhupayCreateOrderResult:
    trade_no: str
    pay_type: str
    pay_info: str


@dataclass(frozen=True)
class ZhupayOrderStatusResult:
    trade_no: str
    api_trade_no: str | None
    buyer: str | None
    money_cents: int | None
    payment_type: str | None
    status: int


def _normalize_pem(raw: str) -> str:
    return raw.strip().replace("\\n", "\n")


@lru_cache(maxsize=1)
def _load_private_key():
    value = _normalize_pem(settings.zhupay_private_key or "")
    if value == "":
        raise ZhupayError("zhupay private key not configured")
    try:
        return serialization.load_pem_private_key(value.encode("utf-8"), password=None)
    except Exception as exc:
        raise ZhupayError("invalid zhupay private key") from exc


@lru_cache(maxsize=1)
def _load_public_key():
    value = _normalize_pem(settings.zhupay_public_key or "")
    if value == "":
        raise ZhupayError("zhupay public key not configured")
    try:
        return serialization.load_pem_public_key(value.encode("utf-8"))
    except Exception as exc:
        raise ZhupayError("invalid zhupay public key") from exc


def is_configured() -> bool:
    return all(
        (
            (settings.zhupay_pid or "").strip(),
            (settings.zhupay_private_key or "").strip(),
            (settings.zhupay_public_key or "").strip(),
            (settings.zhupay_cny_per_credit or "").strip(),
        )
    )


def get_api_base_url() -> str:
    raw = (settings.zhupay_api_base_url or "").strip()
    if raw == "":
        return "https://pay.lxsd.cn"
    return raw.rstrip("/")


def _get_pid() -> str:
    value = (settings.zhupay_pid or "").strip()
    if value == "":
        raise ZhupayError("zhupay pid not configured")
    return value


def _get_cny_per_credit() -> Decimal:
    raw = (settings.zhupay_cny_per_credit or "").strip()
    if raw == "":
        raise ZhupayError("zhupay conversion rate not configured")
    try:
        amount = Decimal(raw)
    except InvalidOperation as exc:
        raise ZhupayError("invalid zhupay conversion rate") from exc
    if amount <= 0:
        raise ZhupayError("invalid zhupay conversion rate")
    return amount


def money_to_cents(raw: str | None) -> int | None:
    if raw is None:
        return None
    value = raw.strip()
    if value == "":
        return None
    try:
        amount = Decimal(value).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)
    except InvalidOperation:
        return None
    return int((amount * 100).to_integral_value(rounding=ROUND_HALF_UP))


def convert_credits_to_money(*, credits: int) -> tuple[str, int]:
    if credits < 0:
        raise ZhupayError("invalid top-up amount")
    amount = (Decimal(credits) * _get_cny_per_credit()).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)
    cents = int((amount * 100).to_integral_value(rounding=ROUND_HALF_UP))
    return (f"{amount:.2f}", cents)


def build_signing_string(params: Mapping[str, object]) -> str:
    items: list[tuple[str, str]] = []
    for key, raw_value in params.items():
        if key in {"sign", "sign_type"}:
            continue
        if raw_value is None:
            continue
        if isinstance(raw_value, (bytes, bytearray, memoryview, dict, list, tuple, set)):
            continue
        value = str(raw_value)
        if value == "":
            continue
        items.append((key, value))
    items.sort(key=lambda item: item[0])
    return "&".join(f"{key}={value}" for key, value in items)


def sign_payload(params: Mapping[str, object]) -> str:
    payload = build_signing_string(params).encode("utf-8")
    signature = _load_private_key().sign(
        payload,
        padding.PKCS1v15(),
        hashes.SHA256(),
    )
    return base64.b64encode(signature).decode("ascii")


def verify_payload(params: Mapping[str, object]) -> bool:
    sign_raw = params.get("sign")
    if not isinstance(sign_raw, str) or sign_raw.strip() == "":
        return False
    normalized_sign = sign_raw.strip().replace(" ", "+")
    sign_type = params.get("sign_type")
    if sign_type is not None and str(sign_type).strip().upper() != SIGN_TYPE:
        return False
    try:
        signature = base64.b64decode(normalized_sign)
        _load_public_key().verify(
            signature,
            build_signing_string(params).encode("utf-8"),
            padding.PKCS1v15(),
            hashes.SHA256(),
        )
        return True
    except Exception:
        return False


def build_signed_request_payload(params: Mapping[str, object]) -> dict[str, str]:
    payload: dict[str, str] = {
        key: str(value)
        for key, value in params.items()
        if value is not None and not isinstance(value, (bytes, bytearray, memoryview, dict, list, tuple, set))
    }
    payload["pid"] = _get_pid()
    payload["timestamp"] = str(int(time.time()))
    payload["sign_type"] = SIGN_TYPE
    payload["sign"] = sign_payload(payload)
    return payload


async def post_api(path: str, params: Mapping[str, object]) -> dict[str, object]:
    payload = build_signed_request_payload(params)
    try:
        async with httpx.AsyncClient(timeout=httpx.Timeout(20.0, connect=10.0)) as client:
            response = await client.post(
                f"{get_api_base_url()}{path}",
                data=payload,
                headers={"content-type": "application/x-www-form-urlencoded"},
            )
    except Exception as exc:
        raise ZhupayError("zhupay request failed") from exc

    if response.status_code >= 400:
        raise ZhupayError("zhupay request failed")

    try:
        data = response.json()
    except Exception as exc:
        raise ZhupayError("invalid zhupay response") from exc
    if not isinstance(data, dict):
        raise ZhupayError("invalid zhupay response")
    has_sign = "sign" in data
    if has_sign and not verify_payload(data):
        raise ZhupayError("invalid zhupay response signature")
    if not has_sign:
        try:
            if int(data.get("code", -1)) == 0:
                raise ZhupayError("invalid zhupay response signature")
        except ZhupayError:
            raise
        except Exception:
            pass
    return data


def _get_required_string(data: Mapping[str, object], key: str) -> str:
    value = data.get(key)
    if not isinstance(value, str) or value.strip() == "":
        raise ZhupayError(f"invalid zhupay response field: {key}")
    return value.strip()


async def create_jump_order(
    *,
    payment_method: str,
    out_trade_no: str,
    notify_url: str,
    return_url: str,
    name: str,
    money: str,
    client_ip: str,
    param: str | None = None,
) -> ZhupayCreateOrderResult:
    if payment_method not in SUPPORTED_PAYMENT_METHODS:
        raise ZhupayError("unsupported payment method")

    response = await post_api(
        "/api/pay/create",
        {
            "method": "jump",
            "type": payment_method,
            "out_trade_no": out_trade_no,
            "notify_url": notify_url,
            "return_url": return_url,
            "name": name,
            "money": money,
            "clientip": client_ip,
            "param": param or "",
        },
    )

    try:
        code = int(response.get("code", -1))
    except Exception as exc:
        raise ZhupayError("invalid zhupay response code") from exc
    if code != 0:
        raise ZhupayError(str(response.get("msg") or "zhupay create failed"))

    return ZhupayCreateOrderResult(
        trade_no=_get_required_string(response, "trade_no"),
        pay_type=_get_required_string(response, "pay_type"),
        pay_info=_get_required_string(response, "pay_info"),
    )


async def query_order(*, out_trade_no: str) -> ZhupayOrderStatusResult:
    response = await post_api("/api/pay/query", {"out_trade_no": out_trade_no})
    try:
        code = int(response.get("code", -1))
    except Exception as exc:
        raise ZhupayError("invalid zhupay response code") from exc
    if code != 0:
        raise ZhupayError(str(response.get("msg") or "zhupay query failed"))

    pid = str(response.get("pid") or "").strip()
    if pid != "" and pid != _get_pid():
        raise ZhupayError("unexpected zhupay pid")

    try:
        status = int(response.get("status", -1))
    except Exception as exc:
        raise ZhupayError("invalid zhupay order status") from exc

    api_trade_no_raw = response.get("api_trade_no")
    buyer_raw = response.get("buyer")
    payment_type_raw = response.get("type")
    money_raw = response.get("money")

    return ZhupayOrderStatusResult(
        trade_no=_get_required_string(response, "trade_no"),
        api_trade_no=api_trade_no_raw.strip() if isinstance(api_trade_no_raw, str) and api_trade_no_raw.strip() else None,
        buyer=buyer_raw.strip() if isinstance(buyer_raw, str) and buyer_raw.strip() else None,
        money_cents=money_to_cents(money_raw if isinstance(money_raw, str) else None),
        payment_type=payment_type_raw.strip() if isinstance(payment_type_raw, str) and payment_type_raw.strip() else None,
        status=status,
    )
