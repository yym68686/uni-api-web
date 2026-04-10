from __future__ import annotations

import unittest

import app.api.router as router_module
from app.api.router import responses, responses_compact, router


class ResponsesRoutesTests(unittest.IsolatedAsyncioTestCase):
    def test_router_registers_responses_routes(self) -> None:
        post_paths = {
            route.path
            for route in router.routes
            if "POST" in getattr(route, "methods", set())
        }

        self.assertIn("/responses", post_paths)
        self.assertIn("/responses/compact", post_paths)

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


if __name__ == "__main__":
    unittest.main()
