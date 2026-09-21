import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TypeSafeChannel } from "./TypeSafeChannel";

let mutations: { method: string; body: Record<string, unknown> }[];
let supported: boolean;
let loseACK: boolean;
beforeEach(() => {
  mutations = [];
  supported = true;
  loseACK = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, options?: RequestInit) => {
      if (url.endsWith("/schema"))
        return Response.json({ create_typesafe: supported });
      if (url.endsWith("/channel-controls"))
        return Response.json({ revision: "snapshot-1" });
      if (url.includes("/operations/"))
        return Response.json({ status: "applied", operation_id: "saved" });
      const body = JSON.parse(String(options?.body));
      mutations.push({ method: options?.method || "GET", body });
      if (options?.method === "PATCH" && loseACK)
        throw new Error("Connection lost");
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
      <TypeSafeChannel
        sources={[
          {
            id: "primary",
            name: "Fugue",
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
  await user.click(screen.getByRole("button", { name: "添加 Jev 渠道" }));
  await user.type(
    screen.getByLabelText("TypeSafe API key"),
    "fixture-typesafe-secret",
  );
  return user;
}
it("creates a key-scoped native channel through validation and persisted settings", async () => {
  const user = await mount();
  expect(screen.queryByRole("option", { name: /other-only/ })).toBeNull();
  await user.click(screen.getByRole("button", { name: "校验并添加" }));
  await screen.findByText(/Jev 渠道已添加/);
  expect(mutations.map((m) => m.method)).toEqual(["POST", "PATCH"]);
  expect(mutations[0].body).toEqual(mutations[1].body);
  expect(mutations[1].body).toMatchObject({
    revision: "snapshot-1",
    changes: [
      {
        provider: "typesafe-jev",
        create_to_key: "key-business",
        set: {
          "/engine": "typesafe",
          "/api": "fixture-typesafe-secret",
          "/model": ["jev-latest", "jev-preview", "jev-1.13.0"],
        },
      },
    ],
    sample: { endpoint: "/v1/systemone", stream: false },
  });
  expect(screen.getByLabelText("TypeSafe API key")).toHaveValue("");
  expect(JSON.stringify(localStorage)).not.toContain("fixture-typesafe-secret");
});
it("rejects an older gateway without sending credentials to an unsupported mutation", async () => {
  supported = false;
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "校验并添加" }));
  await screen.findByText(/请先更新 uni-api/);
  expect(mutations).toHaveLength(0);
});
it("recovers a lost acknowledgement by operation ID without creating again", async () => {
  loseACK = true;
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "校验并添加" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "核对保存结果" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "核对保存结果" }));
  await screen.findByText(/Jev 渠道已添加/);
  expect(mutations.filter((m) => m.method === "PATCH")).toHaveLength(1);
});
