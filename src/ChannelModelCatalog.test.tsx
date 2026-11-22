import { afterEach, expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { ChannelSettings } from "./ChannelSettings";
import type { ManagedChannel } from "./channelManagement";
import type { CatalogModel } from "./ChannelModelCatalog";

afterEach(() => vi.unstubAllGlobals());

it("saves the upstream catalog across reopening without attaching models, then applies explicit Key changes", async () => {
  const model = "vendor/new-model";
  const existing = "gemini-4-carbon";
  const otherKeyModel = "vendor/other-key";
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
  let failedReads = new Set<string>();
  const readFailures: string[] = [];
  const catalogWrites: unknown[] = [];
  const routeWrites: Record<string, unknown>[] = [];
  let releaseOptions!: () => void;
  const optionsReady = new Promise<void>(resolve => { releaseOptions = resolve; });
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const { pathname: path, searchParams } = new URL(input, "http://console.test");
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    const read = path.endsWith("/channel-options") && searchParams.has("keys_only") ? "keys_only" : path.split("/").at(-1)!;
    if (init?.method !== "POST" && failedReads.has(read)) {
      readFailures.push(read);
      return new Response("上游暂时不可用", { status: 503 });
    }
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
    if (path.endsWith("/channel-routes")) return Response.json({ data: [
      ...rows, { provider: "openrouter", model: otherKeyModel, api_key_id: "k2", key_position: 2, position: 1 },
    ], revision: "r1", snapshot_consistent: true, unavailable_keys: [] });
    if (path.endsWith("/api-keys") || path.endsWith("/channel-import-keys")) return Response.json({ keys, data: keys });
    if (path.endsWith("/channel-management/checks")) {
      if (init?.method === "POST") {
        expect(body.targets).toEqual([{ source_id: "fugue", provider: "openrouter", models: [model] }]);
        probed = true;
        return Response.json({ queued: 1 });
      }
      return Response.json({ data: [
        { source_id: "fugue", provider: "openrouter", kind: "availability", model: existing, fingerprint: "fixture",
          state: "done", message: "", history: {}, result: { model: existing, checked_at: 1,
            availability: { status: "error", http_status: 400, message: "上游不支持此模型的请求参数。" } } },
        ...(probed ? [{ source_id: "fugue", provider: "openrouter", kind: "availability", model,
          fingerprint: "fixture", state: "done", history: {}, result: { model, checked_at: 1, availability: { status: "success" } } }] : []),
      ] });
    }
    if (path.endsWith("/channel-management")) {
      if (init?.method === "POST") {
        routeWrites.push(body);
        rows.push({ provider: "openrouter", model: "new-public", upstream_model: model, api_key_id: "k1", key_position: 1, position: 1 });
        return Response.json({ message: "已更新" });
      }
      return Response.json({ data: [item], unavailable_sources: [] });
    }
    return Response.json({ data: [], unavailable_keys: [], unavailable_sources: [] });
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}>
    <Tooltip.Provider delayDuration={0}>
      <ChannelSettings row={{ source_id: "fugue", source_name: "Fugue", provider: "openrouter", model: existing }}
        initialModelTab member={item} />
    </Tooltip.Provider>
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
  expect(dialog.queryByRole("button", { name: "查看检测失败原因" })).not.toBeInTheDocument();
  const input = dialog.getByRole("textbox", { name: `${model} 公开名 1` });
  await user.clear(input);
  await user.type(input, "new-public");
  const filter = dialog.getByRole("combobox", { name: "按接入状态筛选" });
  expect(dialog.getByRole("option", { name: "已接入 (1)" })).toBeInTheDocument();
  expect(dialog.getByRole("option", { name: "已勾选 (2)" })).toBeInTheDocument();
  expect(dialog.getByRole("checkbox", { name: `接入 ${otherKeyModel}` })).not.toBeChecked();
  await user.selectOptions(filter, "saved");
  expect(dialog.queryByRole("checkbox", { name: `接入 ${model}` })).not.toBeInTheDocument();
  expect(dialog.queryByRole("checkbox", { name: `接入 ${otherKeyModel}` })).not.toBeInTheDocument();
  const savedBox = dialog.getByRole("checkbox", { name: `接入 ${existing}` });
  await user.click(savedBox);
  expect(savedBox).not.toBeChecked();
  expect(savedBox).toBeInTheDocument();
  expect(dialog.getByRole("option", { name: "已接入 (1)" })).toBeInTheDocument();
  await user.click(savedBox);
  await user.selectOptions(filter, "attached");
  expect(dialog.getByRole("checkbox", { name: `接入 ${model}` })).toBeChecked();
  await user.selectOptions(filter, "all");
  await user.click(dialog.getByRole("button", { name: "基本设置" }));
  expect(dialog.queryByRole("button", { name: "应用接入变更" })).not.toBeInTheDocument();
  await user.click(dialog.getByRole("button", { name: "模型与接入" }));
  expect(dialog.getByRole("textbox", { name: `${model} 公开名 1` })).toHaveValue("new-public");
  failedReads = new Set(["model-catalog", "channel-options", "checks", "keys_only"]);
  await act(async () => {
    await client.refetchQueries({ predicate: q => [
      "channel-model-catalog", "configured-import-options", "configured-checks", "channel-import-keys",
    ].includes(String(q.queryKey[0])) });
  });
  expect(readFailures.sort()).toEqual(["channel-options", "channel-options", "checks", "checks", "keys_only", "keys_only", "model-catalog", "model-catalog"]);
  await waitFor(() => expect(dialog.getByRole("alert")).toHaveTextContent("暂时无法读取：模型目录、API key 列表、接入配置、模型检测记录。"));
  expect(dialog.getByRole("button", {name: "应用接入变更"})).toBeDisabled();
  expect(dialog.getByRole("option", { name: "已接入 (1)" })).toBeInTheDocument();
  failedReads.clear();
  await user.click(dialog.getByRole("button", { name: "重试读取" }));
  await waitFor(() => expect(dialog.queryByRole("alert")).not.toBeInTheDocument());
  expect(dialog.getByRole("textbox", { name: `${model} 公开名 1` })).toHaveValue("new-public");
  expect(dialog.getByRole("button", {name: "应用接入变更"})).toBeEnabled();
  expect(routeWrites).toHaveLength(0);
  await user.type(dialog.getByRole("searchbox"), "gemini");
  await user.click(dialog.getByRole("button", { name: `配置 ${existing}` }));
  const reason = dialog.getByRole("button", { name: "查看检测失败原因" });
  const failure = "HTTP 400 · 上游不支持此模型的请求参数。";
  expect(screen.queryByText(failure)).not.toBeInTheDocument();
  await user.tab(); // Rerun detection.
  await user.tab(); // Route checkbox.
  await user.tab();
  expect(reason).toHaveFocus();
  expect(await screen.findByRole("tooltip")).toHaveTextContent(failure);
  await user.tab();
  await waitFor(() => expect(screen.queryByRole("tooltip")).not.toBeInTheDocument());
  await user.hover(reason);
  expect(await screen.findByRole("tooltip")).toHaveTextContent(failure);
  expect(dialog.getByLabelText(`${existing} 路由位置`)).toHaveValue("2");
  expect(dialog.getByLabelText(`${existing} 上游端点`)).toHaveValue("responses");
  await user.click(dialog.getByRole("button", { name: "应用接入变更" }));
  await waitFor(() => expect(routeWrites).toHaveLength(1));
  expect(routeWrites[0]).toMatchObject({ source_id: "fugue", api_key_id: "k1", provider: "openrouter", edit_provider: "openrouter",
    models: [existing], model_mappings: { "new-public": model }, positions: { [existing]: 2, "new-public": 1 } });
  expect(routeWrites[0]).not.toHaveProperty("protocols");
  await user.click(screen.getByRole("button", { name: "渠道设置" }));
  dialog = within(screen.getByRole("dialog", { name: "渠道设置" }));
  await dialog.findByRole("option", { name: "已接入 (2)" });
  await user.selectOptions(dialog.getByRole("combobox", { name: "按接入状态筛选" }), "saved");
  await user.type(dialog.getByRole("searchbox"), "new-public");
  expect(dialog.getByRole("checkbox", { name: `接入 ${model}` })).toBeChecked();
  expect(dialog.queryByRole("checkbox", { name: `接入 ${existing}` })).not.toBeInTheDocument();
});
