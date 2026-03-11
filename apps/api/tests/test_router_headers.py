from __future__ import annotations

import unittest

from app.api.upstream_headers import _build_upstream_headers


class _Headers:
    def __init__(self, raw: list[tuple[bytes, bytes]]) -> None:
        self.raw = raw


class _Request:
    def __init__(self, headers: list[tuple[bytes, bytes]]) -> None:
        self.headers = _Headers(headers)


class BuildUpstreamHeadersTests(unittest.TestCase):
    def test_responses_headers_strip_client_auth_headers(self) -> None:
        request = _Request(
            [
                (b"authorization", b"Bearer sk-client"),
                (b"x-api-key", b"sk-client-secondary"),
                (b"openai-beta", b"responses=experimental"),
                (b"content-type", b"application/json"),
            ]
        )

        headers = _build_upstream_headers(request, upstream_api_key="sk-channel")

        self.assertEqual(headers[-1], ("authorization", "Bearer sk-channel"))
        self.assertNotIn(("authorization", "Bearer sk-client"), headers)
        self.assertNotIn(("x-api-key", "sk-client-secondary"), headers)
        self.assertIn(("openai-beta", "responses=experimental"), headers)
        self.assertIn(("content-type", "application/json"), headers)


if __name__ == "__main__":
    unittest.main()
