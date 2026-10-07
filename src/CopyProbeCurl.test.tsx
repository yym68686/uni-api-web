import { afterEach, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CopyProbeCurl } from "./CopyProbeCurl";
import type { Probe } from "./Sub2apiChecks";
afterEach(() => vi.unstubAllGlobals());
const probe: Probe = { id: "probe-one", curl_token: "sealed-request", status: "success", text: "", ttft_ms: 1, duration_ms: 2 };
it.each(["availability", "quality", "tool-use", "compaction"] as const)("copies the server's exact %s request only on click", async kind => {
  const user = userEvent.setup();
  const curl = "curl --url 'https://fixture.test/v1/responses' --header 'Authorization: Bearer fixture-secret' --data-binary '{\"model\":\"fixture\"}'";
  const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
    expect(JSON.parse(String(init?.body))).toEqual({ token: "sealed-request" });
    return Response.json({ curl, original: true });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CopyProbeCurl probe={probe} kind={kind} targets={[]} label="检测" />);
  expect(fetcher).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "检测复制为 curl" }));
  expect(await screen.findByText("已复制")).toBeVisible();
  expect(await navigator.clipboard.readText()).toBe(curl);
  expect(document.body.textContent).not.toContain("fixture-secret");
});
it("labels reconstructed history and resets copied state for another attempt", async () => {
  const user = userEvent.setup();
  const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
    expect(JSON.parse(String(init?.body))).toEqual({probe_id: "probe-one", kind: "compaction", targets: [{account_id:"one",group_id:1}]});
    return Response.json({curl:"# 按当前配置生成\ncurl ...",original:false,message:"历史记录未保存原始请求，已按当前配置生成"});
  });
  vi.stubGlobal("fetch",fetcher);
  const view=render(<CopyProbeCurl probe={{...probe,curl_token:undefined}} kind="compaction" targets={[{account_id:"one",group_id:1}]} label="压缩" />);
  await user.click(screen.getByRole("button"));
  expect(await screen.findByRole("status")).toHaveTextContent("按当前配置生成");
  view.rerender(<CopyProbeCurl probe={{...probe,id:"probe-two"}} kind="compaction" targets={[]} label="压缩" />);
  expect(screen.queryByText("已复制")).not.toBeInTheDocument();
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});
it("does not expose a request for an untested item or claim success on export failure", async () => {
  const user = userEvent.setup();
  vi.stubGlobal("fetch",vi.fn(async()=>new Response("检测记录已更新",{status:404})));
  const view=render(<CopyProbeCurl kind="quality" targets={[]} label="降智" />);
  expect(screen.getByRole("button")).toBeDisabled();
  view.rerender(<CopyProbeCurl probe={probe} kind="quality" targets={[]} label="降智" />);
  await user.click(screen.getByRole("button"));
  expect(await screen.findByRole("alert")).toHaveTextContent("检测记录已更新");
  expect(screen.queryByText("已复制")).not.toBeInTheDocument();
});
