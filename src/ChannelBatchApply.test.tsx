import { afterEach, expect, it, vi } from "vitest";
import {
  render,
  screen,
  within,
  waitFor,
  fireEvent,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelBatchApply } from "./ChannelBatchApply";
import type { BatchDraft } from "./channelBatch";
import type { BatchSnapshot } from "./channelBatch";
import { rememberBatchSnapshot } from "./channelBatchCache";

afterEach(() => vi.unstubAllGlobals());
function setup(remove = false, failSecond = false, preload = false) {
  const revisions: Record<string, string> = { a: "r1", b: "r1" },
    writes: any[] = [];
  const completed = new Set<string>();
  const failures = new Set(failSecond ? ["b"] : []);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input),
        src =
          url.searchParams.get("source_id") ||
          url.pathname.split("/")[4] ||
          "a";
      if (init?.method === "POST") {
        const body = JSON.parse(String(init.body));
        writes.push({ ...body, source_id: src });
        if (body.revision !== revisions[src])
          return new Response("配置已变化，请核对后继续", { status: 409 });
        if (failures.has(src))
          return new Response("来源断开，请核对结果", { status: 502 });
        revisions[src] = "r2";
        completed.add(src);
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
            models: completed.has(source) ? ["new"] : ["old"],
            positions: { old: 1 },
            manageable: true,
            batch_revisions: true,
            atomic_batch: true,
            revision: "r1",
          })),
          unavailable_sources: [],
        });
      if (url.pathname.endsWith("/channel-routes"))
        return Response.json({
          revision: revisions[src],
          manageable: true,
          batch_revisions: true,
          atomic_batch: true,
          snapshot_consistent: true,
          unavailable_keys: [],
          data: [
            {
              provider: "site",
              model: completed.has(src) ? "new" : "old",
              upstream_model: completed.has(src) ? "new" : "old",
              api_key_id: "key",
              key_position: 1,
              position: 1,
            },
          ],
        });
      if (url.pathname.endsWith("/channel-options"))
        return Response.json({
          revision: revisions[src],
          manageable: true,
          batch_revisions: true,
          atomic_batch: true,
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
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  if (preload) {
    const cached: BatchSnapshot = {
      inventory: { data: [], unavailable_sources: [] },
      imports: {
        data: ["a", "b"].map((source) => ({
          source_id: source,
          source_name: source === "a" ? "Fugue" : "DigitalOcean",
          api_key_id: "key",
          key_position: 1,
          key_prefix: "masked",
          provider: "site",
          name: "测试渠道",
          account_id: "account",
          group_id: 1,
          models: completed.has(source) ? ["new"] : ["old"],
          positions: { old: 1 },
          manageable: true,
          revision: "r1",
        })),
        labels: {},
        unavailable_sources: [],
      },
      routes: Object.fromEntries(
        ["a", "b"].map((source) => [
          source,
          {
            revision: "r1",
            manageable: true,
            batch_revisions: true,
            atomic_batch: true,
            snapshot_consistent: true,
            unavailable_keys: [],
            data: [
              {
                provider: "site",
                model: "old",
                upstream_model: "old",
                api_key_id: "key",
                key_position: 1,
                key_prefix: "masked",
                position: 1,
              },
            ],
          },
        ]),
      ),
    };
    rememberBatchSnapshot(client, cached);
  }
  render(
    <QueryClientProvider client={client}>
      <ChannelBatchApply
        remove={remove}
        draft={() => draft}
        onApplied={onApplied}
      />
    </QueryClientProvider>,
  );
  return {
    writes,
    failures,
    onApplied,
    draft,
    client,
    revisions,
    user: userEvent.setup(),
  };
}
it("renders the complete cached preview synchronously with no network reads on click", () => {
  setup(false, false, true);
  const fetch = vi.mocked(globalThis.fetch);
  fetch.mockClear();
  fireEvent.click(
    screen.getByRole("button", { name: "应用全部于所有已保存渠道" }),
  );
  const d = within(screen.getByRole("dialog"));
  expect(d.getByRole("table")).toHaveTextContent("DigitalOcean");
  expect(d.getByRole("table")).toHaveTextContent("Fugue");
  expect(d.getByRole("button", { name: "确认应用 2 个接入" })).toBeEnabled();
  expect(d.queryByText(/正在读取全部来源/)).not.toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
});

it("explains saved failed models that will not be copied to other keys",async()=>{
  const {user,draft,writes}=setup(false,false,true);
  draft.originals={new:"new","gpt-6-luna":"gpt-6-luna"};
  draft.models={...draft.originals};
  draft.retainedOnly={"gpt-6-luna":"gpt-6-luna"};
  await user.click(screen.getByRole("button",{name:"应用全部于所有已保存渠道"}));
  const d=within(screen.getByRole("dialog"));
  expect(d.getByRole("note")).toHaveTextContent("不会添加到其他 API key：gpt-6-luna");
  expect(d.getAllByText("不新增未通过模型：gpt-6-luna")).toHaveLength(2);
  await user.click(d.getByRole("button",{name:"确认应用 2 个接入"}));
  await waitFor(()=>expect(writes).toHaveLength(2));
  expect(writes.every(w=>!('gpt-6-luna' in w.targets[0].models))).toBe(true);
});
it("submits immediately, then reconciles a source rejected for a changed revision", async () => {
  const { user, writes, revisions } = setup(false, false, true);
  revisions.a = "r2";
  await user.click(
    screen.getByRole("button", { name: "应用全部于所有已保存渠道" }),
  );
  const d = within(screen.getByRole("dialog"));
  await user.click(d.getByRole("button", { name: "确认应用 2 个接入" }));
  await d.findByText(/已确认应用 1\/2/);
  expect(d.getByText("配置已变化，请核对后继续")).toBeVisible();
  expect(writes).toHaveLength(2);
  await user.click(d.getByRole("button", { name: "核对剩余接入" }));
  await waitFor(() =>
    expect(d.getByRole("button", { name: "确认应用 1 个接入" })).toBeEnabled(),
  );
  await user.click(d.getByRole("button", { name: "确认应用 1 个接入" }));
  await d.findByText(/全部应用完成/);
  expect(writes[2].revision).toBe("r2");
  expect(writes).toHaveLength(3);
});

it("does not re-read every source before submitting a cached preview", async () => {
  const { writes } = setup(false, false, true);
  const fetcher = vi.mocked(globalThis.fetch);
  fireEvent.click(screen.getByRole("button", { name: "应用全部于所有已保存渠道" }));
  fetcher.mockClear();
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确认应用 2 个接入" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(fetcher.mock.calls.every(([, init]) => init?.method === "POST")).toBe(true);
});
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
  expect(writes[0].targets[0].models).toEqual({ new: "new" });
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
  expect(writes.map((w) => w.part)).toEqual(["delete", "delete"]);
  expect(writes.every((w) => !("models" in w) && !("positions" in w))).toBe(
    true,
  );
});
it("shows a partial result and offers fresh reconciliation without automatic write retry", async () => {
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

it("reconciles partial success and retries only the still-pending source in the same dialog", async () => {
  const { user, writes, failures } = setup(false, true);
  await user.click(
    screen.getByRole("button", { name: "应用全部于所有已保存渠道" }),
  );
  const d = within(screen.getByRole("dialog"));
  await waitFor(() =>
    expect(d.getByRole("button", { name: "确认应用 2 个接入" })).toBeEnabled(),
  );
  await user.click(d.getByRole("button", { name: "确认应用 2 个接入" }));
  await d.findByText(/已确认应用 1\/2/);
  failures.clear();
  await user.click(d.getByRole("button", { name: "核对剩余接入" }));
  await waitFor(() =>
    expect(d.getByRole("button", { name: "确认应用 1 个接入" })).toBeEnabled(),
  );
  expect(d.getByText("已一致，无需重复应用")).toBeVisible();
  await user.click(d.getByRole("button", { name: "确认应用 1 个接入" }));
  await d.findByText(/全部应用完成/);
  expect(writes.map((w) => w.source_id)).toEqual(["a", "b", "b"]);
});
