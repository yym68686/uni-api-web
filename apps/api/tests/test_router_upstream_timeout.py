from __future__ import annotations

import unittest

import httpx

from app.api.router import (
    _llm_upstream_timeout,
    _llm_upstream_timeout_seconds,
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

    def test_llm_upstream_timeout_is_clamped_to_positive_value(self) -> None:
        settings.llm_upstream_timeout_seconds = 0

        timeout = _llm_upstream_timeout()

        self.assertEqual(_llm_upstream_timeout_seconds(), 1.0)
        self.assertEqual(timeout.connect, 1.0)
        self.assertEqual(timeout.read, 1.0)

    def test_translate_read_timeout_to_gateway_timeout(self) -> None:
        settings.llm_upstream_timeout_seconds = 300

        error = _translate_upstream_http_error(httpx.ReadTimeout("timed out"))

        self.assertEqual(error.status_code, 504)
        self.assertEqual(error.detail, "upstream read timeout after 300s")

    def test_translate_connect_error_to_service_unavailable(self) -> None:
        error = _translate_upstream_http_error(httpx.ConnectError("connect failed"))

        self.assertEqual(error.status_code, 503)
        self.assertEqual(error.detail, "upstream unavailable")

    def test_translate_protocol_errors_to_bad_gateway(self) -> None:
        error = _translate_upstream_http_error(httpx.RemoteProtocolError("bad upstream"))

        self.assertEqual(error.status_code, 502)
        self.assertEqual(error.detail, "upstream communication error")


if __name__ == "__main__":
    unittest.main()
