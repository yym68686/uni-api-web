import { expect, it, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Automation } from "./Automation";

const sources = [{ id: "source", name: "主来源", base: "https://example.test", has_storage: true, created_at: 1 }];
afterEach(() => vi.unstubAllGlobals());

it("creates a task with editable thresholds, metric selection, and default safe action", async () => {
  const requests: { path: string; init?: RequestInit }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    requests.push({ path: url, init });
    if (url.endsWith("/v1/automations")) {
      if (init?.method === "POST") return Response.json({ task: { id: "task-1", name: "调序", enabled: false, source_id: "source", key_id: "", model: "gpt-6-astra", interval_seconds: 300, range: "1h", policy: { metrics: ["latency"], min_success_samples: 120, min_cache_samples: 50, min_latency_samples: 50, min_quality_samples: 20, confidence_level: .95, latency_quantile: .5, latency_min_percent: 10, latency_min_ms: 100, success_min_pp: 2, cache_min_pp: 5, quality_min_pp: 5, require_consecutive: 3, cooldown_seconds: 1800, max_moves: 1, action: "suggest", require_all_higher: true } } });
      return Response.json({ data: [] });
    }
    return Response.json({ data: [] });
  }));
  render(<Automation sources={sources} models={["gpt-6-astra"]} />);
  await userEvent.setup().click(screen.getAllByRole("button", { name: /新建任务/ })[0]);
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("任务名称"), "调序");
  await user.selectOptions(screen.getByLabelText("来源"), "source");
  await user.clear(screen.getByLabelText("成功率最小样本"));
  await user.type(screen.getByLabelText("成功率最小样本"), "120");
  await user.click(screen.getByRole("checkbox", { name: "缓存率" }));
  expect(screen.getByRole("combobox", { name: "执行方式" })).toHaveValue("suggest");
  await user.click(screen.getByRole("button", { name: "保存任务" }));
  expect(requests.some(item => item.init?.method === "POST" && String(item.init.body).includes('"min_success_samples":120'))).toBe(true);
});
