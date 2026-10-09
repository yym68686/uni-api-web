import { expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelSortDialog } from "./ChannelSortDialog";
import {
  prepareRouteSorting,
  routeSortSelections,
  useRouteSorting,
} from "./routeSorting";
import type { Channel } from "./types";

const rows = ["b", "a"].map((provider) => ({
  source_id: "s",
  source_name: "Fugue",
  provider,
  provider_name: `渠道${provider}`,
  model: "gpt-6-sol",
})) as Channel[];
const changes = [
  {
    api_key_id: "key",
    key_position: 1,
    model: "gpt-6-sol",
    before: ["a", "hidden", "b"],
    after: ["b", "hidden", "a"],
    upstreams: { a: "gpt-6-sol", b: "gpt-6-sol", hidden: "gpt-6-sol" },
  },
];
function setup(
  ambiguous = false,
  conflict = false,
  prepare?: () => Promise<Response>,
  status?: () => Promise<Response>,
) {
  const onPreview = vi.fn();
  let applied = false;
  const calls: { action: string; [key: string]: unknown }[] = [];
  const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body));
    calls.push(body);
    if (body.action === "prepare")
      return prepare
        ? prepare()
        : Response.json({
            receipt: "signed-receipt",
            revision: "r1",
            changes,
          });
    if (body.action === "status")
      return status
        ? status()
        : Response.json({
            status: conflict ? "conflict" : applied ? "applied" : "before",
            revision: "r2",
          });
    expect(
      localStorage.getItem("uni-console-route-sort-undo:v1:fixture"),
    ).toContain("signed-receipt");
    if (conflict) return new Response("路由配置已变化", { status: 409 });
    applied = body.action === "apply";
    if (ambiguous && applied) throw new TypeError("connection lost");
    return Response.json({
      status: applied ? "applied" : "before",
      revision: "r2",
    });
  });
  vi.stubGlobal("fetch", fetcher);
  function Harness() {
    const routing = useRouteSorting("fixture");
    return (
      <ChannelSortDialog
        rules={[{ field: "quality", direction: "asc" }]}
        onApply={() => {}}
        previewRows={() => rows}
        onPreview={onPreview}
        keyId="s::key"
        routing={routing}
      />
    );
  }
  const mount = () =>
    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Harness />
      </QueryClientProvider>,
    );
  return {
    calls,
    mount,
    onPreview,
    user: userEvent.setup(),
    setConflict: (value: boolean) => {
      conflict = value;
    },
  };
}
it("previews without writing, submits one real change, persists rollback before sending, and can undo after remount", async () => {
  const app = setup();
  let view = app.mount();
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  expect(screen.getByRole("button", { name: "应用排序" })).toBeDisabled();
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "应用排序" })).toBeEnabled(),
  );
  expect(app.calls.map((c) => c.action)).toEqual(["prepare"]);
  expect(app.calls[0]).toMatchObject({
    api_key_id: "s::key",
    models: [{ model: "gpt-6-sol", providers: ["b", "a"] }],
  });
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(app.onPreview).toHaveBeenLastCalledWith(rows);
  expect(
    screen.queryByRole("region", { name: "请求顺序预览" }),
  ).not.toBeInTheDocument();
  await app.user.dblClick(screen.getByRole("button", { name: "应用排序" }));
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await screen.findByText("Fugue：已应用到真实路由");
  expect(app.calls.filter((c) => c.action === "apply")).toHaveLength(1);
  view.unmount();
  view = app.mount();
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  expect(screen.getByRole("button", { name: "撤回排序" })).toBeEnabled();
  await app.user.click(screen.getByRole("button", { name: "撤回排序" }));
  await screen.findByText("Fugue：已恢复应用前顺序");
  expect(app.calls.filter((c) => c.action === "undo")).toHaveLength(1);
  expect(screen.getByRole("button", { name: "撤回排序" })).toBeDisabled();
});
it("requires a new preview after rules change and recovers an ambiguous apply response with a read-only status check", async () => {
  const app = setup(true);
  app.mount();
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "应用排序" })).toBeEnabled(),
  );
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  expect(screen.getByRole("button", { name: "取消预览" })).toBeInTheDocument();
  await app.user.selectOptions(screen.getByLabelText("第 1 排序方向"), "desc");
  expect(screen.getByRole("button", { name: "应用排序" })).toBeDisabled();
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "应用排序" })).toBeEnabled(),
  );
  await app.user.click(screen.getByRole("button", { name: "应用排序" }));
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await screen.findByText(/Fugue：已应用到真实路由/);
  expect(app.calls.map((c) => c.action)).toEqual([
    "prepare",
    "prepare",
    "apply",
    "status",
  ]);
});
it("preserves recovery information on conflicts and never retries a write automatically", async () => {
  const app = setup(false, true);
  app.mount();
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "应用排序" })).toBeEnabled(),
  );
  await app.user.click(screen.getByRole("button", { name: "应用排序" }));
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await screen.findByText(/Fugue：旧排序记录已失效/);
  expect(screen.getByRole("button", { name: "应用排序" })).toBeDisabled();
  expect(app.calls.map((c) => c.action)).toEqual([
    "prepare",
    "apply",
    "status",
  ]);
  expect(
    localStorage.getItem("uni-console-route-sort-undo:v1:fixture"),
  ).toContain("signed-receipt");
});

function seedRecovery(status: "pending" | "error" | "applied" = "error") {
  localStorage.setItem(
    "uni-console-route-sort-undo:v1:fixture",
    JSON.stringify({
      createdAt: 1,
      sources: [
        {
          source: "s",
          name: "Fugue",
          receipt: "signed-receipt",
          revision: "old",
          changes,
          status,
          message: "路由配置在预览或应用后已变化，未执行覆盖；请刷新核对",
        },
      ],
    }),
  );
}

it("rechecks a persisted conflict before a fresh preview and allows apply without dismissing recovery", async () => {
  seedRecovery();
  const app = setup(false, true);
  app.mount();
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  expect(app.calls).toEqual([]);
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "应用排序" })).toBeEnabled(),
  );
  expect(app.calls.map((c) => c.action)).toEqual(["status", "prepare"]);
  app.setConflict(false);
  await app.user.click(screen.getByRole("button", { name: "应用排序" }));
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await screen.findByText("Fugue：已应用到真实路由");
  expect(screen.getByRole("button", { name: "撤回排序" })).toBeEnabled();
  expect(app.calls.filter((c) => c.action === "apply")).toHaveLength(1);
  expect(app.calls.filter((c) => c.action === "undo")).toHaveLength(0);
});

it("recovers a pending result after reload using status only and clears the stale failure message", async () => {
  seedRecovery("pending");
  const app = setup(false, false, undefined, async () =>
    Response.json({ status: "applied", revision: "r2" }),
  );
  app.mount();
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await app.user.click(screen.getByRole("button", { name: "核对排序记录" }));
  await screen.findByText("Fugue：已应用到真实路由");
  expect(screen.queryByText(/未执行覆盖/)).not.toBeInTheDocument();
  expect(app.calls.map((c) => c.action)).toEqual(["status"]);
  expect(screen.getByRole("button", { name: "撤回排序" })).toBeEnabled();
});

it("keeps an uncertain result locked on a network failure and can recover with the check button", async () => {
  seedRecovery();
  let reachable = false;
  const app = setup(false, false, undefined, async () => {
    if (!reachable) throw new TypeError("connection lost");
    return Response.json({ status: "before", revision: "r2" });
  });
  app.mount();
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await screen.findByRole("alert");
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  await screen.findByText(/上次排序结果尚未确认/);
  expect(screen.getByRole("button", { name: "应用排序" })).toBeDisabled();
  expect(app.calls.every((c) => c.action === "status")).toBe(true);
  expect(
    localStorage.getItem("uni-console-route-sort-undo:v1:fixture"),
  ).toContain("signed-receipt");
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  reachable = true;
  await app.user.click(screen.getByRole("button", { name: "核对排序记录" }));
  await screen.findByText("Fugue：已恢复应用前顺序");
  expect(screen.queryByText(/connection lost/)).not.toBeInTheDocument();
  await app.user.click(screen.getByRole("button", { name: "取消预览" }));
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "应用排序" })).toBeEnabled(),
  );
  expect(app.calls.filter((c) => c.action === "prepare")).toHaveLength(1);
  expect(
    app.calls.filter((c) => ["apply", "undo"].includes(String(c.action))),
  ).toHaveLength(0);
});

it("treats a server-rejected receipt as expired, allowing a new preview without an undo write", async () => {
  seedRecovery();
  const app = setup(
    false,
    false,
    undefined,
    async () =>
      new Response("排序记录无效或来源身份已变化，请重新预览", { status: 400 }),
  );
  app.mount();
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await app.user.click(screen.getByRole("button", { name: "核对排序记录" }));
  await screen.findByText(/旧排序记录已失效/);
  expect(screen.getByRole("button", { name: "撤回排序" })).toBeDisabled();
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "应用排序" })).toBeEnabled(),
  );
  expect(
    app.calls.filter((c) => ["apply", "undo"].includes(String(c.action))),
  ).toHaveLength(0);
});
it("partitions all model/source selections and deduplicates endpoint rows without crossing scope", async () => {
  const input = [
    ...rows,
    rows[0],
    { ...rows[0], model: "gpt-6-luna" },
    { ...rows[0], source_id: "other", source_name: "Other" },
  ];
  expect([...routeSortSelections(input)]).toEqual([
    [
      "s",
      [
        { model: "gpt-6-sol", providers: ["b", "a"] },
        { model: "gpt-6-luna", providers: ["b"] },
      ],
    ],
    ["other", [{ model: "gpt-6-sol", providers: ["b"] }]],
  ]);
  const fetcher = vi.fn(async (url: string) =>
    url.includes("other")
      ? new Response("unavailable", { status: 503 })
      : Response.json({ receipt: "token", revision: "r1", changes }),
  );
  vi.stubGlobal("fetch", fetcher);
  const plan = await prepareRouteSorting(input, "");
  expect(plan.errors).toHaveLength(1);
  expect(plan.sources).toHaveLength(1);
});

it("cancels an in-flight preview and ignores its late response without modifying routes", async () => {
  let finish!: (response: Response) => void;
  const app = setup(
    false,
    false,
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  app.mount();
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(app.onPreview).toHaveBeenLastCalledWith(rows);
  await app.user.click(screen.getByRole("button", { name: "取消预览" }));
  expect(app.onPreview).toHaveBeenLastCalledWith(null);
  await act(async () =>
    finish(Response.json({ receipt: "late", revision: "r1", changes })),
  );
  expect(
    screen.queryByRole("button", { name: "应用排序" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "取消预览" }),
  ).not.toBeInTheDocument();
  expect(app.calls.map((c) => c.action)).toEqual(["prepare"]);
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  expect(screen.getByRole("button", { name: "预览" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "应用排序" })).toBeDisabled();
});
