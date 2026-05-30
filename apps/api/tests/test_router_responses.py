from __future__ import annotations

import unittest
import uuid

from fastapi import HTTPException
from starlette.requests import ClientDisconnect

import app.api.router as router_module
from app.api.router import (
    _SseLineBuffer,
    _build_llm_upstream_url,
    _extract_content_generation_status_and_usage,
    _extract_content_generation_task_id,
    _extract_usage_tokens_from_sse_line,
    _parse_content_generation_task_id_timestamp,
    _parse_proxy_request,
    _proxy_content_generation_task_request,
    _read_request_body_or_499,
    content_generation_tasks_create,
    content_generation_tasks_delete,
    content_generation_tasks_get,
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
        get_paths = {
            route.path
            for route in router.routes
            if "GET" in getattr(route, "methods", set())
        }
        delete_paths = {
            route.path
            for route in router.routes
            if "DELETE" in getattr(route, "methods", set())
        }

        self.assertIn("/responses", post_paths)
        self.assertIn("/responses/compact", post_paths)
        self.assertIn("/messages", post_paths)
        self.assertIn("/images/generations", post_paths)
        self.assertIn("/images/edits", post_paths)
        self.assertIn("/contents/generations/tasks", post_paths)
        self.assertIn("/contents/generations/tasks/{task_id}", get_paths)
        self.assertIn("/contents/generations/tasks/{task_id}", delete_paths)

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

    async def test_content_generation_create_uses_task_upstream_path(self) -> None:
        request = object()
        session = object()
        sentinel = object()
        original = router_module._proxy_content_generation_task_request

        async def fake_proxy(
            request_arg: object,
            session_arg: object,
            *,
            method: str,
            upstream_path: str,
            task_id: str | None = None,
        ) -> object:
            self.assertIs(request_arg, request)
            self.assertIs(session_arg, session)
            self.assertEqual(method, "POST")
            self.assertEqual(upstream_path, "/contents/generations/tasks")
            self.assertIsNone(task_id)
            return sentinel

        router_module._proxy_content_generation_task_request = fake_proxy
        try:
            result = await content_generation_tasks_create(request, session)  # type: ignore[arg-type]
        finally:
            router_module._proxy_content_generation_task_request = original

        self.assertIs(result, sentinel)

    async def test_content_generation_get_uses_task_upstream_path(self) -> None:
        request = object()
        session = object()
        sentinel = object()
        original = router_module._proxy_content_generation_task_request

        async def fake_proxy(
            request_arg: object,
            session_arg: object,
            *,
            method: str,
            upstream_path: str,
            task_id: str | None = None,
        ) -> object:
            self.assertIs(request_arg, request)
            self.assertIs(session_arg, session)
            self.assertEqual(method, "GET")
            self.assertEqual(upstream_path, "/contents/generations/tasks/cgt-test")
            self.assertEqual(task_id, "cgt-test")
            return sentinel

        router_module._proxy_content_generation_task_request = fake_proxy
        try:
            result = await content_generation_tasks_get("cgt-test", request, session)  # type: ignore[arg-type]
        finally:
            router_module._proxy_content_generation_task_request = original

        self.assertIs(result, sentinel)

    async def test_content_generation_delete_uses_task_upstream_path(self) -> None:
        request = object()
        session = object()
        sentinel = object()
        original = router_module._proxy_content_generation_task_request

        async def fake_proxy(
            request_arg: object,
            session_arg: object,
            *,
            method: str,
            upstream_path: str,
            task_id: str | None = None,
        ) -> object:
            self.assertIs(request_arg, request)
            self.assertIs(session_arg, session)
            self.assertEqual(method, "DELETE")
            self.assertEqual(upstream_path, "/contents/generations/tasks/cgt-test")
            self.assertEqual(task_id, "cgt-test")
            return sentinel

        router_module._proxy_content_generation_task_request = fake_proxy
        try:
            result = await content_generation_tasks_delete("cgt-test", request, session)  # type: ignore[arg-type]
        finally:
            router_module._proxy_content_generation_task_request = original

        self.assertIs(result, sentinel)

    def test_extract_content_generation_task_response_usage(self) -> None:
        raw = (
            b'{"id":"cgt-test","status":"succeeded",'
            b'"usage":{"completion_tokens":108900,"total_tokens":108900}}'
        )

        self.assertEqual(_extract_content_generation_task_id(raw), "cgt-test")
        self.assertEqual(_extract_content_generation_status_and_usage(raw), ("succeeded", (0, 0, 108900, 108900)))

    def test_parse_content_generation_task_id_timestamp_as_beijing_time(self) -> None:
        parsed = _parse_content_generation_task_id_timestamp("cgt-20260530233354-fmfxq")

        self.assertIsNotNone(parsed)
        self.assertEqual(parsed.isoformat(), "2026-05-30T15:33:54+00:00")

    async def test_content_generation_create_remembers_text_plain_json_task_response(self) -> None:
        class DummyHeaders:
            raw = [(b"content-type", b"application/json")]

            def get(self, name: str, default: str | None = None) -> str | None:
                return {"content-type": "application/json"}.get(name.lower(), default)

        class DummyUrl:
            path = "/v1/contents/generations/tasks"
            query = ""

        class DummyRequest:
            method = "POST"
            headers = DummyHeaders()
            url = DummyUrl()

            async def body(self) -> bytes:
                return b'{"model":"seedance-2-0","content":[{"type":"text","text":"cat"}]}'

        class DummyStreamResponse:
            status_code = 200
            headers = {"content-type": "text/plain; charset=utf-8"}

            async def __aenter__(self):
                return self

            async def __aexit__(self, exc_type, exc, tb) -> None:
                return None

            async def aiter_bytes(self):
                yield b'{"id":"cgt-text-plain"}'

        class DummyClient:
            async def __aenter__(self):
                return self

            async def __aexit__(self, exc_type, exc, tb) -> None:
                return None

            def stream(self, method, url, headers, content):
                self.method = method
                self.url = url
                self.headers = headers
                self.content = content
                return DummyStreamResponse()

        class DummyAsyncClientFactory:
            last_client: DummyClient | None = None

            def __call__(self, *, timeout):
                _ = timeout
                self.last_client = DummyClient()
                return self.last_client

        context = router_module.LlmProxyContext(
            api_key_id=uuid.uuid4(),
            user_id=uuid.uuid4(),
            user_email="user@example.com",
            org_id=uuid.uuid4(),
            model_id="seedance-2-0",
            source_ip="127.0.0.1",
            upstream_base_url="https://upstream.example/v1",
            upstream_api_key="upstream-key",
            pricing=router_module.UsagePricing(None, None),
            channel_id=uuid.uuid4(),
        )
        remembered: list[str] = []
        factory = DummyAsyncClientFactory()
        original_resolve = router_module._resolve_llm_proxy_context
        original_remember = router_module._remember_content_generation_task
        original_record = router_module._record_usage_event_best_effort
        original_async_client = router_module.httpx.AsyncClient

        async def fake_resolve(request, session, *, model_id: str):
            self.assertEqual(model_id, "seedance-2-0")
            return context

        async def fake_remember(*, task_id: str, context: object, status: str | None = None) -> None:
            _ = context, status
            remembered.append(task_id)

        async def fake_record(**kwargs) -> None:
            _ = kwargs

        router_module._resolve_llm_proxy_context = fake_resolve
        router_module._remember_content_generation_task = fake_remember
        router_module._record_usage_event_best_effort = fake_record
        router_module.httpx.AsyncClient = factory  # type: ignore[assignment]
        try:
            response = await _proxy_content_generation_task_request(
                DummyRequest(),  # type: ignore[arg-type]
                object(),  # type: ignore[arg-type]
                method="POST",
                upstream_path="/contents/generations/tasks",
            )
        finally:
            router_module._resolve_llm_proxy_context = original_resolve
            router_module._remember_content_generation_task = original_remember
            router_module._record_usage_event_best_effort = original_record
            router_module.httpx.AsyncClient = original_async_client

        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.body, b'{"id":"cgt-text-plain"}')
        self.assertEqual(remembered, ["cgt-text-plain"])

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
