import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CreateChannel, creationSettings } from "./CreateChannel";

const schema = {
  create_provider: true,
  engines: ["gpt", "claude", "gemini", "typesafe", "aws", "vertex"],
  fields: [
    "engine",
    "base_url",
    "api",
    "model",
    "project_id",
    "region",
    "aws_access_key",
    "aws_secret_key",
    "client_email",
    "private_key",
    "preferences/headers",
    "preferences/cooldown_period",
  ].map((path) => ({ path: "/" + path, type: "string" })),
};
let mutations: { method: string; body: Record<string, any> }[];
let supported: boolean;
let loseACK: boolean;
let reconcileMissing: boolean;
beforeEach(() => {
  mutations = [];
  supported = true;
  loseACK = false;
  reconcileMissing = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, options?: RequestInit) => {
      if (url.endsWith("/schema"))
        return Response.json({ ...schema, create_provider: supported });
      if (url.endsWith("/channel-controls"))
        return Response.json({ revision: "snapshot-1" });
      if (url.includes("/operations/"))
        return reconcileMissing
          ? new Response("Not found", { status: 404 })
          : Response.json({ status: "applied", operation_id: "saved" });
      const body = JSON.parse(String(options?.body));
      mutations.push({ method: options?.method || "GET", body });
      if (options?.method === "PATCH" && loseACK)
        throw Error("Connection lost");
      return Response.json({
        status: options?.method === "PATCH" ? "applied" : "validated",
        operation_id: body.operation_id,
      });
    }),
  );
});
async function mount() {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <CreateChannel
        sources={[
          {
            id: "primary",
            name: "Primary",
            base: "https://gateway.example",
            has_storage: true,
            created_at: 0,
          },
          {
            id: "other",
            name: "Other",
            base: "https://other.example",
            has_storage: true,
            created_at: 0,
          },
        ]}
        keys={[
          {
            key_id: "primary::key-business",
            prefix: "sk-business…",
            position: 1,
            source_id: "primary",
          },
          {
            key_id: "other::key-private",
            prefix: "other-only",
            position: 1,
            source_id: "other",
          },
        ]}
        sourceId="primary"
        keyId="primary::key-business"
      />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "添加渠道" }));
  await waitFor(() => expect(screen.getByLabelText("渠道引擎")).toBeEnabled());
  return user;
}

it("loads caller keys inside channel management without depending on observation filters", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (url.endsWith("/api-keys")) return Response.json({ data: [{ key_id: "caller-key", position: 2, prefix: "masked-caller" }] });
    return Response.json({ create_provider: true, engines: ["gpt"], fields: [] });
  }));
  render(<QueryClientProvider client={new QueryClient()}><CreateChannel sources={[{ id:"primary", name:"Fugue", base:"https://gateway.test", has_storage:true, created_at:0 }]} /></QueryClientProvider>);
  const user=userEvent.setup();
  await user.click(screen.getByRole("button",{name:"添加渠道"}));
  expect(await screen.findByRole("option",{name:"#2 · masked-caller"})).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("调用 API key"),"caller-key");
  expect(screen.getByLabelText("调用 API key")).toHaveValue("caller-key");
});
async function fill(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("渠道名称"), "my-channel.1");
  await user.selectOptions(screen.getByLabelText("渠道引擎"), "claude");
  await user.type(
    screen.getByLabelText("上游地址"),
    "https://gateway.example/v1/messages",
  );
  await user.type(
    screen.getByLabelText("上游 API key"),
    "fixture-secret-a\nfixture-secret-b",
  );
  await user.type(
    screen.getByLabelText("模型与映射"),
    "public-model = vendor/upstream-model\nsecond-model",
  );
}
it("creates an arbitrary provider with multiple keys and model aliases only in the selected calling key", async () => {
  const user = await mount();
  await fill(user);
  expect(screen.queryByRole("option", { name: /other-only/ })).toBeNull();
  await user.click(screen.getByRole("button", { name: "校验并添加" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(mutations.map((m) => m.method)).toEqual(["POST", "PATCH"]);
  expect(mutations[0].body).toEqual(mutations[1].body);
  expect(mutations[1].body).toMatchObject({
    revision: "snapshot-1",
    changes: [
      {
        provider: "my-channel.1",
        create_to_key: "key-business",
        set: {
          "/engine": "claude",
          "/api": ["fixture-secret-a", "fixture-secret-b"],
          "/model": [
            { "vendor/upstream-model": "public-model" },
            "second-model",
          ],
        },
      },
    ],
  });
  expect(JSON.stringify(localStorage)).not.toContain("fixture-secret");
  await user.click(screen.getByRole("button", { name: "添加渠道" }));
  expect(screen.getByLabelText("上游 API key")).toHaveValue("");
});
it("supports cloud credentials and advanced YAML without a dummy API key", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "高级配置" }));
  const textarea = screen.getByLabelText("渠道配置 · JSON / YAML");
  await user.clear(textarea);
  await user.type(
    textarea,
    "provider: cloud\nengine: aws\nbase_url: https://bedrock.example\nmodel:\n  - model-a\naws_access_key: fixture-access\naws_secret_key: fixture-cloud-secret\npreferences:\n  cooldown_period: 30\n",
  );
  await user.click(screen.getByRole("button", { name: "基本配置" }));
  expect(screen.getByLabelText("AWS secret key")).toHaveValue(
    "fixture-cloud-secret",
  );
  expect(screen.getByLabelText("上游 API key")).toHaveValue("");
  await user.click(screen.getByRole("button", { name: "校验并添加" }));
  await waitFor(() => expect(mutations).toHaveLength(2));
  expect(mutations[1].body.changes[0].set).toMatchObject({
    "/engine": "aws",
    "/aws_secret_key": "fixture-cloud-secret",
    "/preferences/cooldown_period": 30,
  });
  expect(mutations[1].body.changes[0].set).not.toHaveProperty("/api");
});
it("does not send credentials when gateway only advertises the older Jev capability", async () => {
  supported = false;
  render(
    <QueryClientProvider client={new QueryClient()}>
      <CreateChannel
        sources={[
          {
            id: "p",
            name: "P",
            base: "https://p.example",
            has_storage: true,
            created_at: 0,
          },
        ]}
        keys={[]}
        sourceId="p"
        keyId=""
      />
    </QueryClientProvider>,
  );
  await userEvent
    .setup()
    .click(screen.getByRole("button", { name: "添加渠道" }));
  await screen.findByText(/该来源尚不支持通用渠道创建/);
  expect(screen.getByRole("button", { name: "校验并添加" })).toBeDisabled();
  expect(mutations).toHaveLength(0);
});
it("retains an uncertain operation across close, failed reconciliation and reopening without resubmitting", async () => {
  const user = await mount();
  await fill(user);
  loseACK = true;
  await user.click(screen.getByRole("button", { name: "校验并添加" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "核对保存结果" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "取消" }));
  await user.click(screen.getByRole("button", { name: "添加渠道" }));
  expect(screen.getByLabelText("上游 API key")).toHaveValue("");
  reconcileMissing = true;
  await user.click(screen.getByRole("button", { name: "核对保存结果" }));
  await screen.findByText("Not found");
  expect(screen.getByRole("button", { name: "核对保存结果" })).toBeEnabled();
  reconcileMissing = false;
  await user.click(screen.getByRole("button", { name: "核对保存结果" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(mutations.filter((m) => m.method === "PATCH")).toHaveLength(1);
});
it("rejects unknown advanced fields and refuses a key from another source", async () => {
  const doc = {
    provider: "channel",
    engine: "gpt",
    base_url: "https://upstream.example",
    model: ["model"],
  };
  expect(() =>
    creationSettings({ ...doc, accidental_field: true }, schema),
  ).toThrow("/accidental_field");
  expect(() =>
    creationSettings({ ...doc, preferences: { unknown: true } }, schema),
  ).toThrow("/preferences/unknown");
  expect(() =>
    creationSettings({ ...doc, provider: "bad/name" }, schema),
  ).toThrow("渠道名称");
  const user = await mount();
  await fill(user);
  await user.selectOptions(screen.getByLabelText("uni-api 来源"), "other");
  await waitFor(() =>
    expect(screen.getByLabelText("调用 API key")).toHaveValue(""),
  );
  expect(screen.getByRole("button", { name: "校验并添加" })).toBeDisabled();
  expect(mutations).toHaveLength(0);
});
