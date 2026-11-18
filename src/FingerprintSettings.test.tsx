import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SourceSettings } from "./SourceSettings";

it("saves only selected fingerprint models and never selects a catalog addition automatically", async () => {
  let supported = ["gpt-6-astra", "gemini-4-argon"], enabled = ["gpt-6-astra"];
  let rejectSave = true;
  const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
    if (init?.method === "PUT") {
      if (rejectSave) return new Response("设置暂不可用", { status: 503 });
      enabled = JSON.parse(String(init.body)).enabled_models;
    }
    return Response.json({ supported_models: supported, enabled_models: enabled, reference_commit: "a".repeat(40) });
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient();
  const view = render(<QueryClientProvider client={client}><SourceSettings sources={[]} onSaved={() => {}} /></QueryClientProvider>);
  try {
    expect(fetcher).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "模型指纹" }));
    const gpt = await screen.findByRole("checkbox", { name: "gpt-6-astra" });
    const gemini = screen.getByRole("checkbox", { name: "gemini-4-argon" });
    expect(gpt).toBeChecked();
    expect(gemini).not.toBeChecked();
    fireEvent.click(gpt);
    fireEvent.click(gemini);
    supported = [...supported, "gpt-new"];
    fireEvent.click(screen.getByRole("button", { name: "重新读取列表" }));
    expect(await screen.findByRole("checkbox", { name: "gpt-new" })).not.toBeChecked();
    expect(gemini).toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: "保存勾选" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("设置暂不可用");
    expect(gemini).toBeChecked();
    rejectSave = false;
    fireEvent.click(screen.getByRole("button", { name: "保存勾选" }));
    expect(await screen.findByText("已保存自动指纹检测设置")).toBeInTheDocument();
    expect(enabled).toEqual(["gemini-4-argon"]);
    expect(screen.getByRole("button", { name: "保存勾选" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "来源" }));
    fireEvent.click(screen.getByRole("button", { name: "模型指纹" }));
    expect(await screen.findByRole("checkbox", { name: "gemini-4-argon" })).toBeChecked();
  } finally {
    view.unmount();
    client.clear();
    vi.unstubAllGlobals();
  }
});
