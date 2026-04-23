from __future__ import annotations

import types
import unittest
import uuid

import app.api.router as router_module
from app.api.llm_proxy import LlmProxyContext, UsagePricing


class _RequestUrl:
    def __init__(self, path: str) -> None:
        self.path = path


class _Request:
    def __init__(
        self,
        *,
        path: str,
        headers: dict[str, str] | None = None,
        client: object | None = None,
        method: str = "POST",
    ) -> None:
        self.method = method
        self.url = _RequestUrl(path)
        self.headers = headers or {}
        self.client = client


class _Session:
    def __init__(self) -> None:
        self.closed = False

    async def close(self) -> None:
        self.closed = True


class LlmRequestLoggingTests(unittest.TestCase):
    def test_log_llm_request_received_includes_user_email(self) -> None:
        context = LlmProxyContext(
            api_key_id=uuid.uuid4(),
            user_id=uuid.uuid4(),
            user_email="alice@example.com",
            org_id=uuid.uuid4(),
            model_id="gpt-4.1",
            source_ip="198.51.100.8",
            upstream_base_url="https://upstream.example.com",
            upstream_api_key="sk-upstream",
            pricing=UsagePricing(input_usd_micros_per_m=None, output_usd_micros_per_m=None),
        )
        request = _Request(path="/v1/responses")

        with self.assertLogs(router_module.logger.name, level="INFO") as captured:
            router_module._log_llm_request_received(request, context=context, stream=True)

        output = captured.output[0]
        self.assertIn("user_email=alice@example.com", output)
        self.assertIn("path=/v1/responses", output)
        self.assertIn("model=gpt-4.1", output)
        self.assertIn("stream=true", output)


class ResolveLlmProxyContextTests(unittest.IsolatedAsyncioTestCase):
    async def test_resolve_context_carries_authenticated_user_email(self) -> None:
        request = _Request(
            path="/v1/chat/completions",
            headers={
                "authorization": "Bearer sk-test",
                "x-forwarded-for": "198.51.100.8, 10.0.0.1",
            },
            client=types.SimpleNamespace(host="203.0.113.9"),
        )
        session = _Session()
        api_key = types.SimpleNamespace(id=uuid.uuid4())
        user = types.SimpleNamespace(
            id=uuid.uuid4(),
            email="alice@example.com",
            balance=100,
            spend_usd_micros_total=0,
            group_name="default",
        )
        membership = types.SimpleNamespace(org_id=uuid.uuid4())
        channel = types.SimpleNamespace(
            base_url="https://upstream.example.com/",
            api_key="sk-upstream",
        )

        original_authenticate_api_key = router_module.authenticate_api_key
        original_require_default_membership = router_module._require_default_membership
        original_get_model_config = router_module.get_model_config
        original_pick_channel_for_group = router_module.pick_channel_for_group

        async def fake_authenticate_api_key(session_arg: object, *, authorization: str | None):
            self.assertIs(session_arg, session)
            self.assertEqual(authorization, "Bearer sk-test")
            return api_key, user

        async def fake_require_default_membership(session_arg: object, *, user_id: uuid.UUID):
            self.assertIs(session_arg, session)
            self.assertEqual(user_id, user.id)
            return membership

        async def fake_get_model_config(session_arg: object, *, org_id: uuid.UUID, model_id: str):
            self.assertIs(session_arg, session)
            self.assertEqual(org_id, membership.org_id)
            self.assertEqual(model_id, "gpt-4.1")
            return None

        async def fake_pick_channel_for_group(session_arg: object, *, org_id: uuid.UUID, group_name: str):
            self.assertIs(session_arg, session)
            self.assertEqual(org_id, membership.org_id)
            self.assertEqual(group_name, "default")
            return channel

        router_module.authenticate_api_key = fake_authenticate_api_key
        router_module._require_default_membership = fake_require_default_membership
        router_module.get_model_config = fake_get_model_config
        router_module.pick_channel_for_group = fake_pick_channel_for_group
        try:
            context = await router_module._resolve_llm_proxy_context(request, session, model_id="gpt-4.1")
        finally:
            router_module.authenticate_api_key = original_authenticate_api_key
            router_module._require_default_membership = original_require_default_membership
            router_module.get_model_config = original_get_model_config
            router_module.pick_channel_for_group = original_pick_channel_for_group

        self.assertEqual(context.api_key_id, api_key.id)
        self.assertEqual(context.user_id, user.id)
        self.assertEqual(context.user_email, "alice@example.com")
        self.assertEqual(context.org_id, membership.org_id)
        self.assertEqual(context.model_id, "gpt-4.1")
        self.assertEqual(context.source_ip, "198.51.100.8")
        self.assertEqual(context.upstream_base_url, "https://upstream.example.com")
        self.assertEqual(context.upstream_api_key, "sk-upstream")
        self.assertTrue(session.closed)

    async def test_resolve_context_prefers_cloudflare_client_ip_headers(self) -> None:
        request = _Request(
            path="/v1/chat/completions",
            headers={
                "authorization": "Bearer sk-test",
                "cf-connecting-ip": "198.51.100.42",
                "x-forwarded-for": "172.71.182.139, 10.0.0.1",
            },
            client=types.SimpleNamespace(host="203.0.113.9"),
        )
        session = _Session()
        api_key = types.SimpleNamespace(id=uuid.uuid4())
        user = types.SimpleNamespace(
            id=uuid.uuid4(),
            email="alice@example.com",
            balance=100,
            spend_usd_micros_total=0,
            group_name="default",
        )
        membership = types.SimpleNamespace(org_id=uuid.uuid4())
        channel = types.SimpleNamespace(
            base_url="https://upstream.example.com/",
            api_key="sk-upstream",
        )

        original_authenticate_api_key = router_module.authenticate_api_key
        original_require_default_membership = router_module._require_default_membership
        original_get_model_config = router_module.get_model_config
        original_pick_channel_for_group = router_module.pick_channel_for_group

        async def fake_authenticate_api_key(session_arg: object, *, authorization: str | None):
            self.assertIs(session_arg, session)
            self.assertEqual(authorization, "Bearer sk-test")
            return api_key, user

        async def fake_require_default_membership(session_arg: object, *, user_id: uuid.UUID):
            self.assertIs(session_arg, session)
            self.assertEqual(user_id, user.id)
            return membership

        async def fake_get_model_config(session_arg: object, *, org_id: uuid.UUID, model_id: str):
            self.assertIs(session_arg, session)
            self.assertEqual(org_id, membership.org_id)
            self.assertEqual(model_id, "gpt-4.1")
            return None

        async def fake_pick_channel_for_group(session_arg: object, *, org_id: uuid.UUID, group_name: str):
            self.assertIs(session_arg, session)
            self.assertEqual(org_id, membership.org_id)
            self.assertEqual(group_name, "default")
            return channel

        router_module.authenticate_api_key = fake_authenticate_api_key
        router_module._require_default_membership = fake_require_default_membership
        router_module.get_model_config = fake_get_model_config
        router_module.pick_channel_for_group = fake_pick_channel_for_group
        try:
            context = await router_module._resolve_llm_proxy_context(request, session, model_id="gpt-4.1")
        finally:
            router_module.authenticate_api_key = original_authenticate_api_key
            router_module._require_default_membership = original_require_default_membership
            router_module.get_model_config = original_get_model_config
            router_module.pick_channel_for_group = original_pick_channel_for_group

        self.assertEqual(context.source_ip, "198.51.100.42")


if __name__ == "__main__":
    unittest.main()
