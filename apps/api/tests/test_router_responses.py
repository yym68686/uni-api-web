from __future__ import annotations

import unittest

from fastapi import HTTPException
from starlette.requests import ClientDisconnect

import app.api.router as router_module
from app.api.router import (
    _SseLineBuffer,
    _build_llm_upstream_url,
    _extract_usage_tokens_from_sse_line,
    _parse_proxy_request,
    _read_request_body_or_499,
    image_edits,
    image_generations,
    messages,
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
        self.assertIn("/messages", post_paths)
        self.assertIn("/images/generations", post_paths)
        self.assertIn("/images/edits", post_paths)

    async def test_read_request_body_maps_client_disconnect_to_499(self) -> None:
        class DisconnectedRequest:
            async def body(self) -> bytes:
                raise ClientDisconnect()

        with self.assertRaises(HTTPException) as ctx:
            await _read_request_body_or_499(DisconnectedRequest())  # type: ignore[arg-type]

        self.assertEqual(ctx.exception.status_code, 499)

    def test_build_llm_upstream_url_preserves_query_string(self) -> None:
        upstream_url = _build_llm_upstream_url(
            upstream_base_url="https://upstream.example/v1",
            upstream_path="/messages",
            query="beta=true",
        )

        self.assertEqual(upstream_url, "https://upstream.example/v1/messages?beta=true")

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

    async def test_messages_uses_messages_upstream_path(self) -> None:
        request = object()
        session = object()
        sentinel = object()
        original = router_module._proxy_responses_request

        async def fake_proxy(request_arg: object, session_arg: object, *, upstream_path: str) -> object:
            self.assertIs(request_arg, request)
            self.assertIs(session_arg, session)
            self.assertEqual(upstream_path, "/messages")
            return sentinel

        router_module._proxy_responses_request = fake_proxy
        try:
            result = await messages(request, session)  # type: ignore[arg-type]
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

    def test_sse_line_buffer_handles_fragmented_lines_without_dropping_data(self) -> None:
        buf = _SseLineBuffer()

        self.assertEqual(buf.feed(b"data: {\"usage\""), [])
        self.assertEqual(buf.feed(b": {\"prompt_tokens\": 3}}\r\n"), [b'data: {"usage": {"prompt_tokens": 3}}'])
        self.assertEqual(buf.feed(b"data: [DONE]\n"), [b"data: [DONE]"])

    def test_extract_usage_tokens_from_sse_line_supports_chat_and_responses_shapes(self) -> None:
        chat_line = (
            b'data: {"usage":{"prompt_tokens":10,"completion_tokens":4,'
            b'"prompt_tokens_details":{"cached_tokens":2},"total_tokens":14}}\n'
        )
        responses_line = (
            b'data: {"response":{"usage":{"input_tokens":8,"output_tokens":3,'
            b'"input_tokens_details":{"cached_tokens":1},"total_tokens":11}}}\n'
        )

        self.assertEqual(_extract_usage_tokens_from_sse_line(chat_line), (10, 2, 4, 14))
        self.assertEqual(_extract_usage_tokens_from_sse_line(responses_line), (8, 1, 3, 11))

    def test_extract_usage_tokens_from_sse_line_supports_anthropic_messages_cache_usage(self) -> None:
        message_start_line = (
            b'data: {"type":"message_start","message":{"usage":{"input_tokens":1454,'
            b'"cache_read_input_tokens":0,"output_tokens":2,'
            b'"cache_creation":{"ephemeral_5m_input_tokens":29314,"ephemeral_1h_input_tokens":0}}}}\n'
        )
        messages_line = (
            b'data: {"type":"message_delta","usage":{"input_tokens":1454,'
            b'"cache_creation_input_tokens":29314,"cache_read_input_tokens":7,'
            b'"output_tokens":117,"output_tokens_details":{"thinking_tokens":114},'
            b'"cache_creation":{"ephemeral_5m_input_tokens":29314}}}\n'
        )

        self.assertEqual(_extract_usage_tokens_from_sse_line(message_start_line), (30768, 0, 2, 30770))
        self.assertEqual(_extract_usage_tokens_from_sse_line(messages_line), (30775, 7, 117, 30892))


if __name__ == "__main__":
    unittest.main()
