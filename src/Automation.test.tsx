import { expect, it, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Automation } from "./Automation";
const sources = [
  {
    id: "source",
    name: "主来源",
    base: "https://example.test",
    has_storage: true,
    created_at: 1,
  },
];
afterEach(() => vi.unstubAllGlobals());
function mockAPI() {
  const requests: { path: string; init?: RequestInit }[] = [];
  let saved: Record<string, unknown> | null = null;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      requests.push({ path: url, init });
      if (url.endsWith("/api-keys"))
        return Response.json({
          data: [{ key_id: "key-fixture", prefix: "masked", position: 1 }],
        });
      if (url.endsWith("/v1/automations")) {
        if (init?.method === "POST") {
          saved = {
            ...JSON.parse(String(init.body)),
            id: "task-1",
            revision: 1,
          };
          return Response.json({ task: saved });
        }
        return Response.json({ data: saved ? [saved] : [] });
      }
      if (init?.method === "PUT") {
        saved = { ...JSON.parse(String(init.body)), revision: 2 };
        return Response.json({ task: saved });
      }
      if (url.includes("/automations/task-1"))
        return Response.json({
          data: [
            {
              id: 1,
              task_id: "task-1",
              task_name: "调序",
              run_at: new Date().toISOString(),
              status: "waiting",
              reason: "支配关系已确认 1/3 次",
              before_order: ["慢渠道", "快渠道"],
              after_order: ["快渠道", "慢渠道"],
              metrics: { comparisons: ["快渠道全面领先"] },
              policy: {},
            },
          ],
        });
      return Response.json({ data: [] });
    }),
  );
  return requests;
}
it("saves explicit scope and parameters, pauses with revision, and displays audit detail", async () => {
  const requests = mockAPI();
  render(<Automation sources={sources} models={["gpt-6-astra"]} />);
  const user = userEvent.setup();
  await user.click(screen.getAllByRole("button", { name: /新建任务/ })[0]);
  expect(
    screen.getByRole("dialog", { name: "新建自动化任务" }),
  ).toBeInTheDocument();
  await user.type(screen.getByLabelText("任务名称"), "调序");
  await screen.findByRole("option", { name: /Key 1/ });
  await user.selectOptions(screen.getByLabelText("API key"), "key-fixture");
  await user.clear(screen.getByLabelText("成功率最小样本"));
  await user.type(screen.getByLabelText("成功率最小样本"), "120");
  await user.click(screen.getByRole("checkbox", { name: "缓存率" }));
  expect(screen.getByRole("combobox", { name: "执行方式" })).toHaveValue(
    "suggest",
  );
  expect(screen.getByLabelText("启用任务")).not.toBeChecked();
  await user.click(screen.getByRole("button", { name: "保存任务" }));
  await screen.findByRole("button", { name: "查看详情" });
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  const post = requests.find((r) => r.init?.method === "POST");
  const body = JSON.parse(String(post?.init?.body));
  expect(body.key_id).toBe("key-fixture");
  expect(body.policy.min_success_samples).toBe(120);
  expect(body.policy.metrics).not.toContain("cache");
  expect(body.policy.endpoint).toBe("/v1/responses");
  await user.click(screen.getByRole("button", { name: "查看详情" }));
  expect(screen.getByRole("dialog")).toHaveTextContent("慢渠道 → 快渠道");
  expect(screen.getByRole("dialog")).toHaveTextContent("等待连续确认");
  await user.click(screen.getByRole("button", { name: "关闭详情" }));
  await user.click(screen.getByRole("button", { name: "启用 调序" }));
  await waitFor(() =>
    expect(requests.some((r) => r.init?.method === "PUT")).toBe(true),
  );
  const put = JSON.parse(
    String(requests.find((r) => r.init?.method === "PUT")?.init?.body),
  );
  expect(put.revision).toBe(1);
  expect(put.enabled).toBe(true);
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /^调序\s*主来源/ }));
  expect(
    screen.getByRole("dialog", { name: "编辑自动化任务" }),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("成功率最小样本")).toHaveValue(120);
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});
it("provides independent quality tasks and refreshes task and audit data", async () => {
  const requests = mockAPI();
  render(<Automation sources={sources} />);
  const user = userEvent.setup();
  await user.click(screen.getAllByRole("button", { name: /新建任务/ })[0]);
  await user.selectOptions(screen.getByLabelText("任务类型"), "quality");
  expect(screen.queryByLabelText("执行方式")).not.toBeInTheDocument();
  expect(screen.getByLabelText("模型")).toBeDisabled();
  expect(screen.getByLabelText("检测并发数")).toHaveValue(4);
  await user.type(screen.getByLabelText("任务名称"), "定时检测");
  await screen.findByRole("option", { name: /Key 1/ });
  await user.selectOptions(screen.getByLabelText("API key"), "key-fixture");
  await user.click(screen.getByRole("button", { name: "保存任务" }));
  const body = JSON.parse(
    String(requests.find((r) => r.init?.method === "POST")?.init?.body),
  );
  expect(body.kind).toBe("quality");
  expect(body.model).toBe("gpt-6-astra");
  const count = requests.length;
  await user.click(screen.getByRole("button", { name: "刷新数据" }));
  await waitFor(() => expect(requests.length).toBeGreaterThan(count));
});
