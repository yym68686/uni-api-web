from __future__ import annotations

import unittest

import app.api.router as router_module
from app.api.router import (
    _parse_proxy_request,
    image_edits,
    image_generations,
    responses,
    responses_compact,
    router,
)


class ResponsesRoutesTests(unittest.IsolatedAsyncioTestCase):
    def test_router_registers_responses_routes(self) -> None:
        post_paths = {
            route.path
            for route in router.routes
            if "POST" in getattr(route, "methods", set())
        }

        self.assertIn("/responses", post_paths)
        self.assertIn("/responses/compact", post_paths)
        self.assertIn("/images/generations", post_paths)
        self.assertIn("/images/edits", post_paths)

    async def test_responses_uses_standard_upstream_path(self) -> None:
        request = object()
        session = object()
        sentinel = object()
        original = router_module._proxy_responses_request

        async def fake_proxy(request_arg: object, session_arg: object, *, upstream_path: str) -> object:
            self.assertIs(request_arg, request)
            self.assertIs(session_arg, session)
            self.assertEqual(upstream_path, "/responses")
            return sentinel

        router_module._proxy_responses_request = fake_proxy
        try:
            result = await responses(request, session)  # type: ignore[arg-type]
        finally:
            router_module._proxy_responses_request = original

        self.assertIs(result, sentinel)

    async def test_image_generations_uses_images_upstream_path(self) -> None:
        request = object()
        session = object()
        sentinel = object()
        original = router_module._proxy_responses_request

        async def fake_proxy(request_arg: object, session_arg: object, *, upstream_path: str) -> object:
            self.assertIs(request_arg, request)
            self.assertIs(session_arg, session)
            self.assertEqual(upstream_path, "/images/generations")
            return sentinel

        router_module._proxy_responses_request = fake_proxy
        try:
            result = await image_generations(request, session)  # type: ignore[arg-type]
        finally:
            router_module._proxy_responses_request = original

        self.assertIs(result, sentinel)

    async def test_image_edits_uses_images_upstream_path_with_multipart_enabled(self) -> None:
        request = object()
        session = object()
        sentinel = object()
        original = router_module._proxy_responses_request

        async def fake_proxy(
            request_arg: object,
            session_arg: object,
            *,
            upstream_path: str,
            allow_multipart: bool = False,
        ) -> object:
            self.assertIs(request_arg, request)
            self.assertIs(session_arg, session)
            self.assertEqual(upstream_path, "/images/edits")
            self.assertTrue(allow_multipart)
            return sentinel

        router_module._proxy_responses_request = fake_proxy
        try:
            result = await image_edits(request, session)  # type: ignore[arg-type]
        finally:
            router_module._proxy_responses_request = original

        self.assertIs(result, sentinel)

    async def test_responses_compact_uses_compact_upstream_path(self) -> None:
        request = object()
        session = object()
        sentinel = object()
        original = router_module._proxy_responses_request

        async def fake_proxy(request_arg: object, session_arg: object, *, upstream_path: str) -> object:
            self.assertIs(request_arg, request)
            self.assertIs(session_arg, session)
            self.assertEqual(upstream_path, "/responses/compact")
            return sentinel

        router_module._proxy_responses_request = fake_proxy
        try:
            result = await responses_compact(request, session)  # type: ignore[arg-type]
        finally:
            router_module._proxy_responses_request = original

        self.assertIs(result, sentinel)

    def test_parse_proxy_request_reads_multipart_model_without_rewriting_body(self) -> None:
        boundary = "----uni-api-test-boundary"
        raw = (
            f"--{boundary}\r\n"
            'Content-Disposition: form-data; name="model"\r\n'
            "\r\n"
            "gpt-image-2\r\n"
            f"--{boundary}\r\n"
            'Content-Disposition: form-data; name="stream"\r\n'
            "\r\n"
            "true\r\n"
            f"--{boundary}\r\n"
            'Content-Disposition: form-data; name="image"; filename="input.png"\r\n'
            "Content-Type: image/png\r\n"
            "\r\n"
            "png-bytes\r\n"
            f"--{boundary}--\r\n"
        ).encode("utf-8")

        parsed = _parse_proxy_request(
            raw,
            content_type=f"multipart/form-data; boundary={boundary}",
            allow_multipart=True,
        )

        self.assertEqual(parsed.model_id, "gpt-image-2")
        self.assertTrue(parsed.stream)


if __name__ == "__main__":
    unittest.main()
