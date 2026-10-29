import { afterEach, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useImportBindings } from "./sub2apiImports";

afterEach(() => vi.unstubAllGlobals());

it("isolates group responses and preserves the shared write invalidation prefix", async () => {
  const reads: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const query = new URL(input).searchParams;
    expect(query.get("view")).toBe("bindings");
    const identity = `${query.get("account_id")}:${query.get("group_id")}`;
    reads.push(identity);
    return Response.json({ data: [], labels: { fixture: { identity } }, unavailable_sources: [] });
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const full = { data: [{ provider: "complete" }] };
  const source = { data: [{ provider: "source" }] };
  client.setQueryData(["sub2api-imports", "bindings"], full);
  client.setQueryData(["sub2api-imports", "bindings", "source"], source);
  function Group({ accountId, groupId }: { accountId: string; groupId: number }) {
    const query = useImportBindings(true, { accountId, groupId });
    return <output aria-label={`${accountId}-${groupId}`}>{query.data?.labels.fixture.identity}</output>;
  }
  render(<QueryClientProvider client={client}><Group accountId="a" groupId={7}/><Group accountId="a" groupId={8}/><Group accountId="b" groupId={7}/></QueryClientProvider>);
  await waitFor(() => expect(screen.getByLabelText("a-7")).toHaveTextContent("a:7"));
  expect(screen.getByLabelText("a-8")).toHaveTextContent("a:8");
  expect(screen.getByLabelText("b-7")).toHaveTextContent("b:7");
  expect(reads.sort()).toEqual(["a:7", "a:8", "b:7"]);
  expect(client.getQueryData(["sub2api-imports", "bindings"])).toBe(full);
  expect(client.getQueryData(["sub2api-imports", "bindings", "source"])).toBe(source);
  await act(async () => { await client.invalidateQueries({ queryKey: ["sub2api-imports"] }); });
  expect(reads.sort()).toEqual(["a:7", "a:7", "a:8", "a:8", "b:7", "b:7"]);
  expect(client.getQueryData(["sub2api-imports", "bindings"])).toBe(full);
});
