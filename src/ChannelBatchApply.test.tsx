import { afterEach, expect, it, vi } from "vitest";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelBatchApply } from "./ChannelBatchApply";
import type { BatchDraft } from "./channelBatch";

afterEach(() => vi.unstubAllGlobals());
function setup(remove = false, failSecond = false) {
  const revisions: Record<string, string> = { a: "r1", b: "r1" },
    writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input),
        src = url.searchParams.get("source_id") || "a";
      if (init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        writes.push(body);
        if (failSecond && body.source_id === "b")
          return new Response("来源断开，请核对结果", { status: 502 });
        revisions[body.source_id] = "r2";
        return Response.json({ revision: "r2" });
      }
      if (url.pathname.endsWith("/channel-management"))
        return Response.json({ data: [], unavailable_sources: [] });
      if (url.pathname.endsWith("/sub2api/channels"))
        return Response.json({
          data: ["a", "b"].map((source) => ({
            source_id: source,
            source_name: source === "a" ? "Fugue" : "DigitalOcean",
            api_key_id: "key",
            key_position: 1,
            provider: "site",
            name: "测试渠道",
            account_id: "account",
            group_id: 1,
            models: ["old"],
            positions: { old: 1 },
            manageable: true,
            batch_revisions: true,
            revision: "r1",
          })),
          unavailable_sources: [],
        });
      if (url.pathname.endsWith("/channel-options"))
        return Response.json({
          revision: revisions[src],
          manageable: true,
          batch_revisions: true,
          channels: [{ provider: "site", model: "old" }],
        });
      throw new Error(url.pathname);
    }),
  );
  const draft: BatchDraft = {
    scope: { kind: "site", account: "account", group: 1 },
    name: "测试渠道",
    part: remove ? "delete" : "all",
    originals: { new: "new" },
    aliases: {},
    models: { new: "new" },
    positions: { new: 1 },
    anchor: { source: "a", key: "key", provider: "site", revision: "r1" },
  };
  const onApplied = vi.fn();
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <ChannelBatchApply
        remove={remove}
        draft={() => draft}
        onApplied={onApplied}
      />
    </QueryClientProvider>,
  );
  return { writes, onApplied, draft, user: userEvent.setup() };
}
it("previews both sources without writing, freezes the draft, and applies only after confirmation", async () => {
  const { user, writes, onApplied, draft } = setup();
  await user.click(
    screen.getByRole("button", { name: "应用全部于所有已保存渠道" }),
  );
  const d = within(screen.getByRole("dialog"));
  await waitFor(() =>
    expect(d.getByRole("button", { name: "确认应用 2 个接入" })).toBeEnabled(),
  );
  expect(d.getByRole("table")).toHaveTextContent("Fugue");
  expect(d.getByRole("table")).toHaveTextContent("DigitalOcean");
  expect(writes).toHaveLength(0);
  draft.models = { unrelated: "unrelated" };
  draft.originals = { unrelated: "unrelated" };
  await user.click(d.getByRole("button", { name: "确认应用 2 个接入" }));
  await d.findByText(/全部应用完成/);
  expect(writes).toHaveLength(2);
  expect(writes[0].model_mappings).toEqual({ new: "new" });
  await user.click(d.getByRole("button", { name: "完成" }));
  expect(onApplied).toHaveBeenCalledOnce();
});
it("deletion lists the full scope, cancel writes nothing, and confirmation deletes all without model changes", async () => {
  const { user, writes } = setup(true);
  await user.click(screen.getByRole("button", { name: "删除所有已保存接入" }));
  let d = within(screen.getByRole("dialog", { name: "删除所有已保存接入" }));
  await waitFor(() =>
    expect(d.getByRole("button", { name: "确认删除 2 个接入" })).toBeEnabled(),
  );
  expect(d.getByText(/基础渠道定义和其他渠道保留/)).toBeVisible();
  await user.click(d.getByRole("button", { name: "取消" }));
  expect(writes).toHaveLength(0);
  await user.click(screen.getByRole("button", { name: "删除所有已保存接入" }));
  d = within(screen.getByRole("dialog"));
  await waitFor(() =>
    expect(d.getByRole("button", { name: "确认删除 2 个接入" })).toBeEnabled(),
  );
  await user.click(d.getByRole("button", { name: "确认删除 2 个接入" }));
  await d.findByText(/全部删除完成/);
  expect(writes.map((w) => w.action)).toEqual(["delete", "delete"]);
  expect(writes.every((w) => !("models" in w) && !("positions" in w))).toBe(
    true,
  );
});
it("shows a partial result and never offers an automatic retry for an ambiguous failure", async () => {
  const { user, writes } = setup(false, true);
  await user.click(
    screen.getByRole("button", { name: "应用全部于所有已保存渠道" }),
  );
  const d = within(screen.getByRole("dialog"));
  await waitFor(() =>
    expect(d.getByRole("button", { name: "确认应用 2 个接入" })).toBeEnabled(),
  );
  await user.click(d.getByRole("button", { name: "确认应用 2 个接入" }));
  await d.findByText(/已确认应用 1\/2/);
  expect(d.getByText("来源断开，请核对结果")).toBeVisible();
  expect(d.queryByRole("button", { name: /确认应用/ })).not.toBeInTheDocument();
  expect(writes).toHaveLength(2);
});
