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
it("detects all filtered pages concurrently, without retrying failures", async () => {
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
  expect(max).toBe(27);
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

it("stops every in-flight batch request and clears pending state", async () => {
  let started = 0,
    aborted = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((_input: string, init?: RequestInit) => {
      if (init?.method !== "POST")
        return Promise.resolve(new Response(JSON.stringify({ data: [] })));
      started++;
      return new Promise<Response>((_resolve, reject) => {
        init.signal!.addEventListener(
          "abort",
          () => {
            aborted++;
            reject(new DOMException("Aborted", "AbortError"));
          },
          { once: true },
        );
      });
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result, unmount } = renderHook(
    () => useChannelChecks("cancel-test", true),
    { wrapper },
  );
  let batch: Promise<void>;
  act(() => {
    batch = result.current.runAll([
      row("do", "a"),
      row("do", "b"),
      row("do", "c"),
    ]);
  });
  await waitFor(() => expect(started).toBe(3));
  await act(async () => {
    result.current.stop();
    await batch!;
  });
  expect(aborted).toBe(3);
  expect(result.current.pending.size).toBe(0);
  expect(result.current.batch).toBeNull();
  expect(result.current.results.size).toBe(0);
  unmount();
  client.clear();
});

it("replaces an older local check error when newer persisted history arrives", async () => {
  let persisted: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) =>
      init?.method === "POST"
        ? new Response("fixture failure", { status: 502 })
        : new Response(JSON.stringify({ data: persisted })),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result, unmount } = renderHook(
    () => useChannelChecks("latest-test", true),
    { wrapper },
  );
  await act(async () => {
    await result.current.run(row("one", "shared"));
  });
  const id = JSON.stringify(["one", "shared"]);
  const failed = result.current.results.get(id)!;
  expect(failed.verdict).toBe("error");
  persisted = [
    {
      ...failed,
      verdict: "pass",
      text: "未知",
      checked_at: failed.checked_at + 1,
    },
  ];
  await act(async () => {
    await result.current.refetch();
  });
  await waitFor(() =>
    expect(result.current.results.get(id)?.verdict).toBe("pass"),
  );
  unmount();
  client.clear();
});

it("keeps a restarted batch pending when canceled responses arrive late", async () => {
  const requests: {
    signal: AbortSignal;
    provider: string;
    finish: (value: Response) => void;
  }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((_input: string, init?: RequestInit) => {
      if (init?.method !== "POST")
        return Promise.resolve(new Response(JSON.stringify({ data: [] })));
      return new Promise<Response>((finish) =>
        requests.push({
          signal: init.signal!,
          provider: JSON.parse(init.body as string).provider,
          finish,
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
  const { result, unmount } = renderHook(
    () => useChannelChecks("restart-test", true),
    { wrapper },
  );
  let oldBatch!: Promise<void>, newBatch!: Promise<void>;
  act(() => {
    oldBatch = result.current.runAll([row("one", "a"), row("one", "b")]);
  });
  await waitFor(() => expect(requests).toHaveLength(2));
  act(() => {
    result.current.stop();
    newBatch = result.current.runAll([row("one", "a")]);
  });
  await waitFor(() => expect(requests).toHaveLength(3));
  expect(requests.slice(0, 2).every((request) => request.signal.aborted)).toBe(
    true,
  );
  expect(requests[2].signal.aborted).toBe(false);
  const response = (provider: string, verdict: string) =>
    new Response(
      JSON.stringify({
        source_id: "one",
        provider,
        model: "gpt-6-astra",
        verdict,
        text: verdict === "pass" ? "未知" : "2024-06",
        checked_at: 1,
        duration_ms: 10,
      }),
    );
  await act(async () => {
    requests[0].finish(response("a", "pass"));
    requests[1].finish(response("b", "pass"));
    await oldBatch;
  });
  expect(result.current.results.size).toBe(0);
  expect(result.current.batch).toEqual({ done: 0, total: 1 });
  expect(result.current.pending).toEqual(
    new Set([JSON.stringify(["one", "a"])]),
  );
  await act(async () => {
    requests[2].finish(response("a", "fail"));
    await newBatch;
  });
  expect(
    result.current.results.get(JSON.stringify(["one", "a"]))?.verdict,
  ).toBe("fail");
  expect(result.current.pending.size).toBe(0);
  expect(result.current.batch).toBeNull();
  unmount();
  client.clear();
});
