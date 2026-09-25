import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import { ChannelDetailChecks } from "./ChannelDetailChecks";
import type { Channel } from "./types";
import type { ConfiguredCheck } from "./channelManagement";
const row = {
  source_id: "do",
  source_name: "DigitalOcean",
  provider: "sub2api-key-owned",
  model: "public-alias",
  upstream_model: "real-upstream",
} as Channel;
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <ChannelDetailChecks row={row} />
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
      targets: [
        { source_id: "do", provider: row.provider, models: ["public-alias"] },
      ],
    },
    {
      kind: "tool-use",
      targets: [
        { source_id: "do", provider: row.provider, models: ["public-alias"] },
      ],
    },
    {
      kind: "quality",
      targets: [
        { source_id: "do", provider: row.provider, models: ["gpt-6-astra"] },
      ],
    },
    {
      kind: "compaction",
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
  await waitFor(() => expect(screen.getByText(/检测状态读取失败/)).toBeVisible());
  expect(screen.getByRole("button", { name: "降智检测" })).toBeEnabled();
});
it("keeps queued work disabled across reads and shows the corresponding completed result", async () => {
  const result = {
    status: "unsupported",
    checked_at: 10,
    model: "public-alias",
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
  expect(
    within(button.closest(".detail-check-item") as HTMLElement).getByRole(
      "status",
    ),
  ).toHaveTextContent("不支持工具调用");
  expect(screen.getByText("No exec")).toBeVisible();
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
      <ChannelDetailChecks row={astra} />
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
