import { afterEach, expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelSettings } from "./ChannelSettings";
import type { ManagedChannel } from "./channelManagement";
import type { CatalogModel } from "./ChannelModelCatalog";

afterEach(() => vi.unstubAllGlobals());

it("saves the upstream catalog across reopening without attaching models, then applies explicit Key changes", async () => {
  const model = "vendor/new-model";
  const existing = "gemini-4-carbon";
  const item: ManagedChannel = {
    kind: "configured", source_id: "fugue", source_name: "Fugue", provider: "openrouter", name: "OpenRouter",
    models: [existing], probe_fingerprint: "fixture", account_id: "", account_ids: [], engine: "openrouter",
    group_id: 0, api_key_id: "k1", key_position: 1, key_prefix: "masked", positions: {}, revision: "r1", manageable: true,
  };
  const keys = [{ key_id: "k1", position: 1, prefix: "masked" }];
  const rows = [
    { provider: "fallback", model: existing, upstream_model: existing, api_key_id: "k1", key_position: 1, position: 1 },
    { provider: "openrouter", model: existing, upstream_model: existing, api_key_id: "k1", key_position: 1, position: 2,
      upstream_protocol: "responses", upstream_protocol_options: ["responses", "chat", "systemone", "anthropic"] },
  ];
  let directory: CatalogModel[] = [];
  let probed = false;
  const catalogWrites: unknown[] = [];
  const routeWrites: Record<string, unknown>[] = [];
  let releaseOptions!: () => void;
  const optionsReady = new Promise<void>(resolve => { releaseOptions = resolve; });
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const path = new URL(input, "http://console.test").pathname;
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    if (path.endsWith("/model-catalog")) {
      if (init?.method === "POST") {
        catalogWrites.push(body);
        directory = body.action === "refresh" ? [{ model, observed: true }] : [...directory, { model: body.model, manual: true }];
      }
      return Response.json({ models: directory, refreshed_at: directory.length ? "2026-11-22T00:00:00Z" : null });
    }
    if (path.endsWith("/channel-settings")) {
      const doc = { provider: "openrouter", base_url: "https://provider.example/api/v1/systemone", model: [existing] };
      return Response.json({ provider: "openrouter", revision: "r1", kind: "imported", api_key_id: "k1", base: doc, effective: doc,
        override_paths: [], affected_keys: [{ key_id: "k1", models: [existing] }], available_keys: keys,
        schema: { engines: ["openrouter"], fields: [{ path: "/base_url", type: "string", group: "基本与模型" }, { path: "/model", type: "models", group: "基本与模型" }] } });
    }
    if (path.endsWith("/channel-options")) {
      await optionsReady;
      return Response.json({ revision: "r1", keys, channels: rows, model_upstream_protocols: true });
    }
    if (path.endsWith("/channel-routes")) return Response.json({ data: rows, revision: "r1", snapshot_consistent: true, unavailable_keys: [] });
    if (path.endsWith("/api-keys") || path.endsWith("/channel-import-keys")) return Response.json({ keys, data: keys });
    if (path.endsWith("/channel-management/checks")) {
      if (init?.method === "POST") {
        expect(body.targets).toEqual([{ source_id: "fugue", provider: "openrouter", models: [model] }]);
        probed = true;
        return Response.json({ queued: 1 });
      }
      return Response.json({ data: probed ? [{ source_id: "fugue", provider: "openrouter", kind: "availability", model,
        fingerprint: "fixture", state: "done", history: {}, result: { model, checked_at: 1, availability: { status: "success" } } }] : [] });
    }
    if (path.endsWith("/channel-management")) {
      if (init?.method === "POST") {
        routeWrites.push(body);
        return Response.json({ message: "已更新" });
      }
      return Response.json({ data: [item], unavailable_sources: [] });
    }
    return Response.json({ data: [], unavailable_keys: [], unavailable_sources: [] });
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}>
    <ChannelSettings row={{ source_id: "fugue", source_name: "Fugue", provider: "openrouter", model: existing }}
      initialModelTab member={item} />
  </QueryClientProvider>);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "渠道设置" }));
  let dialog = within(screen.getByRole("dialog", { name: "渠道设置" }));
  const existingBox = await dialog.findByRole("checkbox", { name: `接入 ${existing}` });
  expect(dialog.queryByText(/此 API key 专用渠道|仅作用于 Key/)).not.toBeInTheDocument();
  const search = dialog.getByRole("searchbox");
  await user.type(search, "carbon");
  expect(existingBox).toBeDisabled();
  await act(async () => { releaseOptions(); });
  await waitFor(() => expect(existingBox).toBeEnabled());
  expect(existingBox).toBeChecked();
  expect(dialog.getByRole("searchbox")).toBe(search);
  expect(search).toHaveValue("carbon");
  await user.clear(search);
  expect(dialog.getByRole("button", { name: "应用接入变更" })).toBeDisabled();
  expect(dialog.queryByRole("button", { name: "保存并应用" })).not.toBeInTheDocument();
  expect(dialog.queryByRole("textbox", { name: "手工添加上游模型 ID" })).not.toBeInTheDocument();
  await user.click(dialog.getByRole("button", { name: "从上游读取模型" }));
  expect(await dialog.findByRole("checkbox", { name: `接入 ${model}` })).not.toBeChecked();
  await user.click(dialog.getByRole("button", { name: "手工添加" }));
  await user.type(dialog.getByRole("textbox", { name: "手工添加上游模型 ID" }), "manual/one");
  await user.click(dialog.getByRole("button", { name: "添加到目录" }));
  expect(await dialog.findByText("manual/one")).toBeVisible();
  expect(routeWrites).toHaveLength(0);
  expect(catalogWrites).toEqual([{ action: "refresh", revision: "r1" }, { action: "add", revision: "r1", model: "manual/one" }]);

  await user.click(dialog.getByRole("button", { name: "关闭渠道设置" }));
  client.removeQueries({ queryKey: ["channel-model-catalog"] });
  await user.click(screen.getByRole("button", { name: "渠道设置" }));
  dialog = within(screen.getByRole("dialog", { name: "渠道设置" }));
  expect(await dialog.findByText("manual/one")).toBeVisible();
  const pending = await dialog.findByRole("checkbox", { name: `接入 ${model}` });
  expect(pending).toBeDisabled();
  await user.click(dialog.getByRole("button", { name: `检测模型 ${model}` }));
  await waitFor(() => expect(dialog.getByRole("checkbox", { name: `接入 ${model}` })).toBeEnabled());
  await user.click(dialog.getByRole("checkbox", { name: `接入 ${model}` }));
  await user.click(dialog.getByRole("button", { name: `配置 ${model}` }));
  const input = dialog.getByRole("textbox", { name: `${model} 公开名 1` });
  await user.clear(input);
  await user.type(input, "new-public");
  await user.click(dialog.getByRole("button", { name: "基本设置" }));
  expect(dialog.queryByRole("button", { name: "应用接入变更" })).not.toBeInTheDocument();
  await user.click(dialog.getByRole("button", { name: "模型与接入" }));
  expect(dialog.getByRole("textbox", { name: `${model} 公开名 1` })).toHaveValue("new-public");
  await user.type(dialog.getByRole("searchbox"), "gemini");
  await user.click(dialog.getByRole("button", { name: `配置 ${existing}` }));
  expect(dialog.getByLabelText(`${existing} 路由位置`)).toHaveValue("2");
  expect(dialog.getByLabelText(`${existing} 上游端点`)).toHaveValue("responses");
  await user.click(dialog.getByRole("button", { name: "应用接入变更" }));
  await waitFor(() => expect(routeWrites).toHaveLength(1));
  expect(routeWrites[0]).toMatchObject({ source_id: "fugue", api_key_id: "k1", provider: "openrouter", edit_provider: "openrouter",
    models: [existing], model_mappings: { "new-public": model }, positions: { [existing]: 2, "new-public": 1 } });
  expect(routeWrites[0]).not.toHaveProperty("protocols");
});
