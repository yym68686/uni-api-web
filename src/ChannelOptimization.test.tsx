import { expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelOptimization } from "./ChannelOptimization";
import {
  optimizationFixture,
  modelCheck,
} from "./channelOptimization.test-data";

it("previews without writes and applies only explicitly checked changes", async () => {
  const snapshot = optimizationFixture();
  snapshot.accounts[0].targets[0].models = [
    modelCheck("old", "error"),
    modelCheck("new"),
  ];
  const fetch = vi.fn(async (_url: string, _init?: RequestInit) =>
    Response.json({ revision: "r2" }),
  );
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={client}>
      <ChannelOptimization snapshot={() => snapshot} />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
  expect(
    screen.getByRole("dialog", { name: "优化渠道模型" }),
  ).toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "一键应用优化" })).toBeDisabled();
  await user.click(
    screen.getByRole("checkbox", { name: "取消 old · Fugue Key 1 Site-0.1" }),
  );
  expect(screen.getByText(/此 API key 将移除该渠道接入/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "一键应用优化" }));
  await screen.findByText("已应用");
  const body = JSON.parse(fetch.mock.calls[0][1]!.body as string);
  expect(body).toMatchObject({
    revision: "r1",
    part: "optimize",
    targets: [
      { provider: "p", current: { old: "old" }, models: {}, positions: {} },
    ],
  });
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "一键应用优化" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "完成" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});

it("supports event selection and cancel; freezes the preview scope while open", async () => {
  const snapshot = optimizationFixture();
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelOptimization snapshot={() => snapshot} />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
  snapshot.filters.model = "unrelated";
  expect(
    screen.getByRole("checkbox", { name: "新增 new · Fugue Key 1 Site-0.1" }),
  ).toBeInTheDocument();
  await user.click(screen.getByRole("checkbox", { name: "选择全部优化变更" }));
  await user.click(screen.getByRole("checkbox", { name: "新增可用模型" }));
  expect(screen.getByRole("button", { name: "一键应用优化" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "取消" }));
  expect(fetch).not.toHaveBeenCalled();
});

it("reports a rejected atomic source batch once without accusing every selected channel", async () => {
  const snapshot = optimizationFixture();
  snapshot.imports.data.push({
    ...snapshot.imports.data[0],
    provider: "peer-binding",
    name: "Other Site",
  });
  snapshot.routes.s.data.push({
    ...snapshot.routes.s.data[0],
    provider: "peer-binding",
    position: 2,
  });
  const fetch = vi.fn(
    async () =>
      new Response(
        "新增或重命名模型必须在当前渠道检测可用，请先完成检测；未通过：new",
        { status: 400 },
      ),
  );
  vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelOptimization snapshot={() => snapshot} />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
  await user.click(screen.getByRole("checkbox", { name: "选择全部优化变更" }));
  await user.click(screen.getByRole("button", { name: "一键应用优化" }));
  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent("Fugue：本批次优化未确认");
  expect(alert).toHaveTextContent("此提示不代表每个渠道都未通过检测");
  expect(alert.closest(".optimization-binding")).toBeNull();
  expect(screen.getAllByRole("alert")).toHaveLength(1);
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "一键应用优化" })).toBeDisabled();
});
