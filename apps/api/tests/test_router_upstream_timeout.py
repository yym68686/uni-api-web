from __future__ import annotations

import time
import unittest
import uuid
from types import SimpleNamespace

import httpx

import app.api.router as router_module
from app.api.llm_proxy import LlmProxyContext, UsagePricing
from app.api.router import (
    _llm_upstream_timeout,
    _llm_upstream_timeout_seconds,
    _record_upstream_http_error_usage,
    _translate_upstream_http_error,
)
from app.core.config import settings


class UpstreamTimeoutTests(unittest.TestCase):
    def setUp(self) -> None:
        self._original_timeout_seconds = settings.llm_upstream_timeout_seconds

    def tearDown(self) -> None:
        settings.llm_upstream_timeout_seconds = self._original_timeout_seconds

    def test_llm_upstream_timeout_uses_configured_seconds(self) -> None:
        settings.llm_upstream_timeout_seconds = 123

        timeout = _llm_upstream_timeout()

        self.assertEqual(_llm_upstream_timeout_seconds(), 123.0)
        self.assertEqual(timeout.connect, 10.0)
        self.assertEqual(timeout.read, 123.0)
        self.assertEqual(timeout.write, 123.0)
        self.assertEqual(timeout.pool, 123.0)

    def test_llm_upstream_timeout_can_be_disabled(self) -> None:
        settings.llm_upstream_timeout_seconds = 0

        timeout = _llm_upstream_timeout()

        self.assertIsNone(_llm_upstream_timeout_seconds())
        self.assertEqual(timeout.connect, 10.0)
        self.assertIsNone(timeout.read)
        self.assertIsNone(timeout.write)
        self.assertIsNone(timeout.pool)

    def test_translate_read_timeout_to_gateway_timeout(self) -> None:
        settings.llm_upstream_timeout_seconds = 300

        error = _translate_upstream_http_error(httpx.ReadTimeout("timed out"))

        self.assertEqual(error.status_code, 504)
        self.assertEqual(error.detail, "upstream read timeout after 300s")

    def test_translate_read_timeout_when_timeout_is_disabled(self) -> None:
        settings.llm_upstream_timeout_seconds = 0

        error = _translate_upstream_http_error(httpx.ReadTimeout("timed out"))

        self.assertEqual(error.status_code, 504)
        self.assertEqual(error.detail, "upstream read timeout")

    def test_translate_connect_error_to_service_unavailable(self) -> None:
        error = _translate_upstream_http_error(httpx.ConnectError("connect failed"))

        self.assertEqual(error.status_code, 503)
        self.assertEqual(error.detail, "upstream unavailable")

    def test_translate_protocol_errors_to_bad_gateway(self) -> None:
        error = _translate_upstream_http_error(httpx.RemoteProtocolError("bad upstream"))

        self.assertEqual(error.status_code, 502)
        self.assertEqual(error.detail, "upstream communication error")


class UpstreamHttpErrorUsageTests(unittest.IsolatedAsyncioTestCase):
    async def test_records_translated_upstream_error_status(self) -> None:
        calls: list[dict[str, object]] = []
        original = router_module._record_usage_event_best_effort

        async def fake_record_usage_event_best_effort(**kwargs: object) -> None:
            calls.append(kwargs)

        router_module._record_usage_event_best_effort = fake_record_usage_event_best_effort
        try:
            context = LlmProxyContext(
                api_key_id=uuid.uuid4(),
                user_id=uuid.uuid4(),
                user_email="user@example.com",
                org_id=uuid.uuid4(),
                model_id="gpt-test",
                source_ip="203.0.113.10",
                upstream_base_url="https://upstream.example",
                upstream_api_key="sk-test",
                pricing=UsagePricing(input_usd_micros_per_m=None, output_usd_micros_per_m=None),
            )
            request = SimpleNamespace(url=SimpleNamespace(path="/v1/responses"))

            error = await _record_upstream_http_error_usage(
                request=request,  # type: ignore[arg-type]
                context=context,
                exc=httpx.ReadTimeout("timed out"),
                started=time.perf_counter(),
                is_streaming=False,
            )
        finally:
            router_module._record_usage_event_best_effort = original

        self.assertEqual(error.status_code, 504)
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["model_id"], "gpt-test")
        self.assertEqual(calls[0]["ok"], False)
        self.assertEqual(calls[0]["status_code"], 504)
        self.assertEqual(calls[0]["request_endpoint"], "/v1/responses")
        self.assertEqual(calls[0]["is_streaming"], False)
        self.assertEqual(calls[0]["recompute_cost"], False)


if __name__ == "__main__":
    unittest.main()
