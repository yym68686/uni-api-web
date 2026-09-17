import { afterEach, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { checkTargets, useChannelChecks } from "./ChannelChecks";
import type { Channel } from "./types";

const row = (source_id: string, provider: string, model = "gpt-6-astra") =>
  ({ source_id, provider, model }) as Channel;
afterEach(() => vi.unstubAllGlobals());
it("deduplicates providers across models, preserving source isolation and filter order", () => {
  expect(
    checkTargets([
      row("do", "second"),
      row("do", "first"),
      row("do", "second", "other"),
      row("fugue", "second"),
    ]).map((r) => [r.source_id, r.provider]),
  ).toEqual([
    ["do", "second"],
    ["do", "first"],
    ["fugue", "second"],
  ]);
});
it("detects all filtered pages with bounded concurrency, without retrying failures", async () => {
  let active = 0,
    max = 0;
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method !== "POST")
        return new Response(JSON.stringify({ data: [] }));
      const { provider } = JSON.parse(init.body as string);
      active++;
      max = Math.max(max, active);
      calls.push(provider);
      await new Promise((resolve) => setTimeout(resolve, 3));
      active--;
      if (provider === "p2")
        return new Response("fixture failure", { status: 502 });
      return new Response(
        JSON.stringify({
          source_id: "do",
          provider,
          model: "gpt-6-astra",
          verdict: "pass",
          text: "未知",
          checked_at: 1,
          duration_ms: 3,
        }),
      );
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result, unmount } = renderHook(() => useChannelChecks("test", true), {
    wrapper,
  });
  const rows = Array.from({ length: 27 }, (_, i) => row("do", `p${i}`));
  rows.push(row("do", "p0", "other"));
  await act(async () => {
    await result.current.runAll(rows);
  });
  expect(calls).toHaveLength(27);
  expect(new Set(calls).size).toBe(27);
  expect(max).toBe(2);
  expect(result.current.results.size).toBe(27);
  expect(
    [...result.current.results.values()].find((r) => r.provider === "p2")
      ?.verdict,
  ).toBe("error");
  expect(result.current.pending.size).toBe(0);
  expect(result.current.batch).toBeNull();
  await waitFor(() =>
    expect(
      result.current.results.get(JSON.stringify(["do", "p0"]))?.verdict,
    ).toBe("pass"),
  );
  unmount();
  client.clear();
});
