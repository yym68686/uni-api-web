import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { expect, it, vi } from "vitest";
import { Sub2apiImport } from "./Sub2apiImport";
import { ConfiguredChannelDialog } from "./ChannelRoutes";
import { useChannelImportKeys } from "./channelImportKeys";
import type { ManagedChannel } from "./channelManagement";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { SubImportsQuery } from "./sub2apiImports";
import type { ConsoleSourcesQuery } from "./consoleSources";

const model = "gpt-6-sol";
const sources = [
  { id: "fugue", name: "Fugue" },
  { id: "do", name: "DigitalOcean" },
];
const keys = (source: string) =>
  [1, 2].map((n) => ({
    key_id: `${source}-key-${n}`,
    prefix: `${source}-masked-${n}`,
    position: n,
  }));
const result = {
  model,
  checked_at: 1,
  availability: { status: "success", text: "ok", duration_ms: 1, ttft_ms: 1 },
  quality: {
    status: "not_applicable",
    text: "",
    duration_ms: 0,
    ttft_ms: null,
  },
  verdict: "not_applicable",
};
const members = sources.map((s) => ({
  source_id: s.id,
  source_name: s.name,
  provider: "native",
  name: "native",
  models: [model],
  kind: "configured",
})) as ManagedChannel[];

it.each(["site", "native"])(
  "%s selector switches synchronously using cached keys while slow routes and key refresh stay pending",
  async (kind) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    for (const s of sources)
      client.setQueryData(
        ["channel-import-keys", s.id],
        { keys: keys(s.id) },
        { updatedAt: Date.now() - 60_000 },
      );
    const pending: {
      source: string;
      key: string;
      resolve: (response: Response) => void;
    }[] = [];
    const writes: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string, init?: RequestInit) => {
        const u = new URL(input);
        if (init?.method === "POST") {
          writes.push(JSON.parse(String(init.body)));
          return Response.json({ message: "saved" });
        }
        if (u.pathname.endsWith("/channel-options")) {
          if (u.searchParams.has("keys_only"))
            return new Promise<Response>(() => {});
          return new Promise<Response>((resolve) =>
            pending.push({
              source: u.searchParams.get("source_id")!,
              key: u.searchParams.get("api_key_id")!,
              resolve,
            }),
          );
        }
        if (u.pathname.endsWith("/channel-management/checks"))
          return Response.json({
            data: members.map((m) => ({
              source_id: m.source_id,
              provider: m.provider,
              kind: "model",
              model,
              fingerprint: "",
              state: "done",
              message: "",
              result,
            })),
          });
        return Response.json({
          data: u.pathname.endsWith("/channel-management") ? members : [],
          unavailable_keys: [],
          unavailable_sources: [],
        });
      }),
    );
    render(
      <QueryClientProvider client={client}>
        <Tooltip.Provider>
          {kind === "site" ? (
            <Sub2apiImport
              account={{ id: "site", name: "Site" } as SubAccount}
              target={
                {
                  group_id: 1,
                  name: "Group",
                  models: [{ model, state: "done", message: "", result }],
                } as SubTarget
              }
              imports={
                {
                  data: { data: [], labels: {}, unavailable_sources: [] },
                  isPending: false,
                  isError: false,
                } as unknown as SubImportsQuery
              }
              sources={{ data: { data: sources } } as ConsoleSourcesQuery}
              close={() => {}}
            />
          ) : (
            <ConfiguredChannelDialog
              item={{ ...members[0], members }}
              close={() => {}}
            />
          )}
        </Tooltip.Provider>
      </QueryClientProvider>,
    );
    const select = await screen.findByLabelText("添加到 uni-api 来源");
    const key = screen.getByLabelText("添加到 API key");
    fireEvent.change(select, { target: { value: "fugue" } });
    expect(key).toBeEnabled();
    expect(
      within(key).getByRole("option", { name: "Key 1 · fugue-masked-1" }),
    ).toBeInTheDocument();
    expect(
      within(key).queryByRole("option", { name: /do-masked/ }),
    ).not.toBeInTheDocument();
    expect(pending).toHaveLength(0); // no heavyweight configuration read before choosing a key
    fireEvent.change(key, { target: { value: "fugue-key-1" } });
    await waitFor(() => expect(pending).toHaveLength(1));
    expect(key).toBeEnabled();
    expect(screen.getByLabelText("渠道添加位置")).toBeDisabled();
    expect(screen.getByRole("button", { name: "添加到渠道" })).toBeDisabled();
    fireEvent.change(select, { target: { value: "do" } });
    expect(key).toBeEnabled();
    expect(key).toHaveValue("");
    expect(
      within(key).getByRole("option", { name: "Key 1 · do-masked-1" }),
    ).toBeInTheDocument();
    expect(
      within(key).queryByRole("option", { name: /fugue-masked/ }),
    ).not.toBeInTheDocument();
    fireEvent.change(key, { target: { value: "do-key-2" } });
    await waitFor(() => expect(pending).toHaveLength(2));
    // A late reply from the old source must not unlock saving or change options.
    await act(async () =>
      pending[0].resolve(
        Response.json({
          supported: true,
          manageable: true,
          revision: "old-fugue",
          keys: keys("fugue"),
          channels: [],
        }),
      ),
    );
    expect(key).toHaveValue("do-key-2");
    expect(screen.getByRole("button", { name: "添加到渠道" })).toBeDisabled();
    await act(async () =>
      pending[1].resolve(
        Response.json({
          supported: true,
          manageable: true,
          revision: "fresh-do",
          keys: keys("do"),
          channels: [],
        }),
      ),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "添加到渠道" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "添加到渠道" }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toMatchObject({
      source_id: "do",
      api_key_id: "do-key-2",
      revision: "fresh-do",
    });
  },
);

function Directory() {
  const [query] = useChannelImportKeys(["do"]);
  return (
    <select aria-label="directory" disabled={!query.data?.keys.length}>
      {query.data?.keys.map((key) => (
        <option key={key.key_id} value={key.key_id}>
          {key.prefix}
        </option>
      ))}
    </select>
  );
}

it("reuses a complete login-scoped directory, normalizes source prefixes and preserves empty-route keys", () => {
  const client = new QueryClient();
  client.setQueryData(["keys", "session:all"], {
    data: [...keys("fugue"), ...keys("do")].map((key) => ({
      ...key,
      source_id: key.key_id.startsWith("do") ? "do" : "fugue",
      key_id: `${key.key_id.startsWith("do") ? "do" : "fugue"}::${key.key_id}`,
    })),
    unavailable_sources: [],
  });
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  render(
    <QueryClientProvider client={client}>
      <Directory />
    </QueryClientProvider>,
  );
  expect(screen.getByLabelText("directory")).toBeEnabled();
  expect(
    screen.getAllByRole("option").map((o) => (o as HTMLOptionElement).value),
  ).toEqual(["do-key-1", "do-key-2"]);
  expect(fetch).not.toHaveBeenCalled();
});

it("does not seed partial directories and stops displaying revoked keys on successful refresh", async () => {
  const client = new QueryClient();
  client.setQueryData(["keys", "session:all"], {
    data: keys("do").map((key) => ({ ...key, source_id: "do" })),
    unavailable_sources: ["DigitalOcean"],
  });
  let finish!: (response: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>((resolve) => (finish = resolve))),
  );
  render(
    <QueryClientProvider client={client}>
      <Directory />
    </QueryClientProvider>,
  );
  expect(screen.getByLabelText("directory")).toBeDisabled();
  await act(async () => finish(Response.json({ keys: keys("do") })));
  await waitFor(() => expect(screen.getByLabelText("directory")).toBeEnabled());
  await act(async () => {
    void client.invalidateQueries({ queryKey: ["channel-import-keys", "do"] });
  });
  expect(screen.getByLabelText("directory")).toBeEnabled();
  await act(async () => finish(Response.json({ keys: [] })));
  await waitFor(() =>
    expect(screen.getByLabelText("directory")).toBeDisabled(),
  );
  expect(screen.queryByRole("option")).not.toBeInTheDocument();
});

it("does not reuse directory data invalidated by a source credential change", async () => {
  const client = new QueryClient();
  client.setQueryData(["keys", "session:all"], {data: keys("do").map(key => ({...key, source_id: "do"}))});
  await client.invalidateQueries({queryKey: ["keys"]});
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
  render(<QueryClientProvider client={client}><Directory /></QueryClientProvider>);
  expect(screen.getByLabelText("directory")).toBeDisabled();
  expect(screen.queryByRole("option")).not.toBeInTheDocument();
});
