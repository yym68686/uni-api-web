import * as Tooltip from "@radix-ui/react-tooltip";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import { ChannelDetailChecks } from "./ChannelDetailChecks";
import type { Channel } from "./types";
import type { ConfiguredCheck } from "./channelManagement";
import type { ChannelCheck } from "./ChannelChecks";
const row = {
  source_id: "do",
  source_name: "DigitalOcean",
  provider: "sub2api-key-owned",
  model: "codex-public-alias",
  upstream_model: "real-upstream",
} as Channel;
function mount(channel = row, quality?: ChannelCheck) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <Tooltip.Provider><ChannelDetailChecks row={channel} owner="audit" quality={quality} /></Tooltip.Provider>
      </QueryClientProvider>,
    ),
  };
}
it("independently queues each probe against the exact source, installed channel and public model", async () => {
  const writes: any[] = [];
  let finish!: () => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        const body = JSON.parse(String(init.body));
        writes.push(body);
        if (body.kind === "availability")
          await new Promise<void>((resolve) => {
            finish = resolve;
          });
        return Response.json({ queued: 1 });
      }
      return Response.json({ data: [] });
    }),
  );
  mount();
  const user = userEvent.setup();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "可用性检测" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "可用性检测" }));
  expect(screen.getByRole("button", { name: "可用性检测" })).toBeDisabled();
  for (const name of ["Tool use 检测", "降智检测", "远程压缩检测"]) {
    expect(screen.getByRole("button", { name })).toBeEnabled();
    await user.click(screen.getByRole("button", { name }));
  }
  expect(writes).toEqual([
    {
      kind: "availability",
      manual: true,
      targets: [
        { source_id: "do", provider: row.provider, models: ["codex-public-alias"] },
      ],
    },
    {
      kind: "tool-use",
      manual: true,
      targets: [
        { source_id: "do", provider: row.provider, models: ["codex-public-alias"] },
      ],
    },
    {
      kind: "quality",
      manual: true,
      targets: [
        { source_id: "do", provider: row.provider, models: ["gpt-6-astra"] },
      ],
    },
    {
      kind: "compaction",
      manual: true,
      targets: [{ source_id: "do", provider: row.provider, models: [] }],
    },
  ]);
  await act(async () => finish());
});
it("allows probes before history finishes loading, even if that read fails", async () => {
  const writes: any[] = [];
  let finish!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn(async (_path: string, init?: RequestInit) => {
    if (init?.method === "POST") {
      writes.push(JSON.parse(String(init.body)));
      return Response.json({ queued: 1 });
    }
    return new Promise<Response>((resolve) => { finish = resolve; });
  }));
  mount();
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "可用性检测" });
  expect(button).toBeEnabled();
  await user.click(button);
  expect(writes).toHaveLength(1);
  await act(async () => finish(new Response("读取失败", { status: 503 })));
  await waitFor(() => expect(screen.getByRole("button", {name:/检测状态读取失败/})).toBeVisible());
  expect(screen.getByRole("button", { name: "降智检测" })).toBeEnabled();
});
it("keeps queued work disabled across reads and shows the corresponding completed result", async () => {
  const result = {
    status: "unsupported",
    checked_at: 10,
    model: "codex-public-alias",
    message: "No exec",
    attempts: [],
  };
  const record = {
    source_id: "do",
    provider: row.provider,
    kind: "tool-use",
    model: row.model,
    state: "running",
    message: "",
    fingerprint: "",
    result: null,
  } as ConfiguredCheck;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      Response.json({
        data: [
          record,
          { ...record, source_id: "fugue", state: "done", result },
        ],
      }),
    ),
  );
  const { client } = mount();
  const button = await screen.findByRole("button", { name: "Tool use 检测" });
  await waitFor(() => expect(button).toHaveAttribute("aria-busy", "true"));
  expect(button).toBeDisabled();
  expect(screen.queryByText(/不支持工具调用/)).not.toBeInTheDocument();
  act(() =>
    client.setQueryData(["configured-checks"], {
      data: [{ ...record, state: "done", result }],
    }),
  );
  await waitFor(() => expect(button).toBeEnabled());
  const check = within(button.closest(".detail-check-item") as HTMLElement);
  expect(check.getByText("不支持工具调用")).toBeVisible();
  expect(check.getByText("No exec").closest(".sr-only")).not.toBeNull();
});
it("shows submission errors without disabling unrelated probes", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init?: RequestInit) =>
      init?.method === "POST"
        ? new Response("来源暂不可用", { status: 503 })
        : Response.json({ data: [] }),
    ),
  );
  mount();
  const user = userEvent.setup();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "远程压缩检测" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "远程压缩检测" }));
  expect(await screen.findByRole("alert")).toBeVisible();
  for (const name of [
    "可用性检测",
    "Tool use 检测",
    "降智检测",
    "远程压缩检测",
  ])
    expect(screen.getByRole("button", { name })).toBeEnabled();
});

it("does not let Astra quality work disable its independent availability check", async () => {
  const astra = { ...row, model: "gpt-6-astra" };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      Response.json({
        data: [
          {
            source_id: astra.source_id,
            provider: astra.provider,
            model: astra.model,
            kind: "model",
            state: "running",
            result: null,
            message: "",
          },
        ],
      }),
    ),
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <Tooltip.Provider><ChannelDetailChecks row={astra} owner="audit" /></Tooltip.Provider>
    </QueryClientProvider>,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "降智检测" })).toHaveAttribute(
      "aria-busy",
      "true",
    ),
  );
  expect(screen.getByRole("button", { name: "可用性检测" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Tool use 检测" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "远程压缩检测" })).toBeEnabled();
});

it.each(["claude-opus-4-8","gemini-3.1-pro","deepseek"])('does not offer Codex capability probes for %s', async model=>{
 const fetcher=vi.fn(async(_input: string, _init?: RequestInit)=>Response.json({data:[]}));vi.stubGlobal('fetch',fetcher);
 mount({...row,model});
 const user=userEvent.setup();
 for(const name of ['Tool use 检测','远程压缩检测']) {
  const button=screen.getByRole('button',{name});expect(button).toBeDisabled();await user.click(button);
 }
 await waitFor(() => expect(screen.getAllByText('不适用')).toHaveLength(5));
 expect(screen.getByRole('button',{name:'可用性检测'})).toBeEnabled();
 expect(fetcher.mock.calls.some(([,init])=>init?.method==='POST')).toBe(false);
});

it("keeps fingerprint actions and the latest quality summary in the independent check list", async () => {
  const writes: unknown[] = [];
  const model = "gpt-6.2-sol";
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url.endsWith("/model-fingerprints")) {
      if (init?.method === "POST") writes.push(JSON.parse(String(init.body)));
      return Response.json({data: [], supported_models: [model]});
    }
    return Response.json({data: []});
  }));
  mount({...row, model}, {
    source_id: row.source_id!, provider: row.provider, model: "gpt-6-astra",
    verdict: "pass", checked_at: 100, duration_ms: 50, text: "",
    history: {total: 2, successful: 2, passed: 2},
  });
  const section = screen.getByRole("region", {name: "渠道独立检测"});
  const action = within(section).getByRole("button", {name: "指纹检测"});
  const grid = action.closest(".detail-check-grid") as HTMLElement;
  expect(within(grid).getByRole("button", {name: "降智检测"})).toBeVisible();
  expect(within(grid).getByText("不降智")).toHaveClass("positive");
  expect(within(grid).getByText("100.0%")).toBeVisible();
  expect(section.lastElementChild).toBe(grid);
  await waitFor(() => expect(action).toBeEnabled());
  await userEvent.setup().click(action);
  expect(writes).toEqual([{targets: [{source_id: row.source_id, provider: row.provider, model}]}]);
});

it("independently submits Fast and Ultrafast with the verified inventory scope and preserves separate statuses", async () => {
  const model = "gpt-6.2-sol";
  const channel = {...row, model, upstream_model: model, engine:"codex"};
  const scope = "a".repeat(64);
  const now = Math.floor(Date.now()/1000);
  let checks: Partial<ConfiguredCheck>[] = [];
  const writes: unknown[] = [];
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url.includes("/service-tier-target?")) return Response.json({
      applicable:true, scope, group_results: {fast:{status:"supported", observed:"priority", checked_at:now - 10}},
    });
    if (init?.method === "POST") {
      const body = JSON.parse(String(init.body));
      writes.push(body);
      checks = [...checks, {...channel, kind:body.kind, fingerprint:scope, state:"queued", result:null, message:""}];
      return Response.json({queued:1});
    }
    return Response.json({data: url.includes("channel-management/checks") ? checks : []});
  }));
  const {client} = mount(channel);
  const fast = screen.getByRole("button",{name:"Fast 检测"});
  const ultra = screen.getByRole("button",{name:"Ultrafast 检测"});
  await waitFor(() => expect(fast).toBeEnabled());
  expect(within(fast.closest(".detail-check-item") as HTMLElement).getByText("已验证支持")).toHaveAttribute("title", expect.stringContaining("分组检测"));
  expect(within(ultra.closest(".detail-check-item") as HTMLElement).getByText("未检测")).toBeVisible();
  const user = userEvent.setup();
  await user.click(fast);
  await waitFor(() => expect(fast).toBeDisabled());
  expect(ultra).toBeEnabled();
  await user.click(ultra);
  await waitFor(() => expect(ultra).toBeDisabled());
  expect(writes).toEqual(["fast","ultrafast"].map(kind => ({kind,manual:true,confirmed:true,
    targets:[{source_id:row.source_id,provider:row.provider,models:[model],expected_scope:scope}]})));
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getAllByText("已排队")).toHaveLength(2));
  checks = checks.map(c => ({...c,state:"done",result:{status:c.kind==="fast"?"supported":"not_honored",
    checked_at:now,binding:scope,observed:c.kind==="fast"?"priority":"default",
    ...(c.kind==="fast"?{verified_binding:scope,verified_at:now}: {})}}));
  await act(async () => { await client.invalidateQueries({queryKey:["configured-checks"]}); });
  await waitFor(() => expect(within(fast.closest(".detail-check-item") as HTMLElement).getByText("已验证支持")).toHaveClass("positive"));
  await waitFor(() => expect(within(ultra.closest(".detail-check-item") as HTMLElement).getByText("未命中")).toHaveClass("detail-check-warning"));
  expect(fast).toBeEnabled();
  expect(ultra).toBeEnabled();
  // A stale channel scope falls back to the matching group result.
  checks = checks.map(c => ({...c, fingerprint:"b".repeat(64)}));
  await act(async () => { await client.invalidateQueries({queryKey:["configured-checks"]}); });
  await waitFor(() => expect(within(fast.closest(".detail-check-item") as HTMLElement).getByText("已验证支持")).toHaveAttribute("title", expect.stringContaining("分组检测")));
  expect(within(ultra.closest(".detail-check-item") as HTMLElement).getByText("未确认")).toBeVisible();
  vi.unstubAllGlobals();
});
