import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { expect, it, vi } from "vitest";
import { ConfiguredChannelDialog } from "./ChannelRoutes";
import { Sub2apiImport } from "./Sub2apiImport";
import type { ManagedChannel } from "./channelManagement";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { SubImportsQuery } from "./sub2apiImports";
import type { ConsoleSourcesQuery } from "./consoleSources";

it.each(["native", "site"])(
  "%s editor sends only verified additions to other keys while retaining a saved failed model locally",
  async (kind) => {
    const sources = [
      { id: "a", name: "DigitalOcean" },
      { id: "b", name: "Fugue" },
    ];
    const target = {
      group_id: 3,
      name: "稳定渠道",
      models: ["gpt-5.5", "gpt-6-luna", "gpt-6-sol"].map((model) => ({
        model,
        state: "done",
        message: "",
        result: {
          model,
          checked_at: 10,
          verdict: "not_applicable",
          availability: {
            status: model === "gpt-6-luna" ? "error" : "success",
            text: "",
            ttft_ms: 1,
            duration_ms: 1,
          },
          quality: {
            status: "not_applicable",
            text: "",
            ttft_ms: null,
            duration_ms: 0,
          },
        },
      })),
    } as SubTarget;
    const account = {
      id: "acct",
      name: "ccttt99",
      targets: [target],
    } as SubAccount;
    const member = (s: (typeof sources)[number]) =>
      ({
        kind: "configured",
        source_id: s.id,
        source_name: s.name,
        provider: "stable",
        name: "stable",
        models: ["gpt-5.5"],
        model_mappings: {},
        engine: "gpt",
        binding_status: "matched",
        bound_keys: [{ account_id: "acct", group_id: 3 }],
        account_id: "acct",
        group_id: 3,
        probe_fingerprint: "",
      }) as ManagedChannel;
    const members = sources.map(member);
    const provider = kind === "native" ? "stable" : "site-copy";
    const rows = (source: string) =>
      (source === "a" ? ["gpt-5.5", "gpt-6-luna"] : ["gpt-5.5"]).map(
        (model) => ({
          provider,
          model,
          upstream_model: model,
          api_key_id: "key",
          key_position: 1,
          key_prefix: `masked-${source}`,
          position: 1,
        }),
      );
    const imports = {
      data:
        kind === "native"
          ? []
          : sources.map((s) => ({
              source_id: s.id,
              source_name: s.name,
              provider,
              account_id: "acct",
              group_id: 3,
              api_key_id: "key",
              key_position: 1,
              key_prefix: `masked-${s.id}`,
              name: "稳定渠道",
              models: rows(s.id).map((r) => r.model),
              positions: Object.fromEntries(
                rows(s.id).map((r) => [r.model, 1]),
              ),
              revision: "r1",
              manageable: true,
            })),
      labels: {},
      unavailable_sources: [],
    };
    const routeData = (s: string) => ({
      data: rows(s),
      revision: "r1",
      manageable: true,
      batch_revisions: true,
      atomic_batch: true,
      snapshot_consistent: true,
      unavailable_keys: [],
    });
    const writes: any[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string, init?: RequestInit) => {
        const u = new URL(input);
        if (init?.method === "POST") {
          writes.push({
            source: u.pathname.split("/")[4],
            ...JSON.parse(String(init.body)),
          });
          return Response.json({ revision: "r2" });
        }
        if (u.pathname.endsWith("/channel-management"))
          return Response.json({
            data: kind === "native" ? members : [],
            unavailable_sources: [],
          });
        if (u.pathname.endsWith("/sub2api/channels"))
          return Response.json(imports);
        if (u.pathname.endsWith("/sub2api/accounts"))
          return Response.json({ data: [account] });
        if (u.pathname.endsWith("/channel-routes"))
          return Response.json(routeData(u.pathname.split("/")[4]));
        if (u.pathname.endsWith("/channel-options")) {
          const s = u.searchParams.get("source_id")!;
          return Response.json({
            supported: true,
            manageable: true,
            revision: "r1",
            keys: [{ key_id: "key", position: 1, prefix: `masked-${s}` }],
            channels: rows(s),
          });
        }
        return Response.json({ data: [] });
      }),
    );
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    client.setQueryData(["sub2api"], { data: [account] });
    client.setQueryData(["sub2api-imports"], imports);
    client.setQueryData(["channel-management"], {
      data: kind === "native" ? members : [],
      unavailable_sources: [],
    });
    for (const s of sources)
      client.setQueryData(["channel-routes", s.id], routeData(s.id));
    render(
      <QueryClientProvider client={client}>
        <Tooltip.Provider>
          {kind === "native" ? (
            <ConfiguredChannelDialog
              item={{ ...members[0], members }}
              initialEdit={rows("a")}
              close={() => {}}
            />
          ) : (
            <Sub2apiImport
              account={account}
              target={target}
              imports={
                {
                  data: imports,
                  isPending: false,
                  isError: false,
                } as unknown as SubImportsQuery
              }
              sources={{ data: { data: sources } } as ConsoleSourcesQuery}
              close={() => {}}
            />
          )}
        </Tooltip.Provider>
      </QueryClientProvider>,
    );
    const user = userEvent.setup();
    if (kind === "site")
      await user.click(
      await screen.findByRole("button", { name: "编辑" }),
      );
    await waitFor(() =>
      expect(
        screen.getByRole("checkbox", { name: "gpt-6-luna" }),
      ).toBeChecked(),
    );
    await waitFor(() =>
      expect(screen.getByRole("checkbox", { name: "gpt-6-sol" })).toBeEnabled(),
    );
    await user.click(screen.getByRole("checkbox", { name: "gpt-6-sol" }));
    await user.click(
      screen.getByRole("button", { name: "将模型勾选应用于所有已保存渠道" }),
    );
    const d = within(
      screen.getByRole("dialog", { name: "模型勾选 · 应用于所有已保存渠道" }),
    );
    expect(d.getByRole("note")).toHaveTextContent("gpt-6-luna");
    const tableRows = within(d.getByRole("table")).getAllByRole("row");
    expect(tableRows[1]).toHaveTextContent("2 → 3 个模型");
    expect(tableRows[1]).not.toHaveTextContent("不新增未通过模型");
    expect(tableRows[2]).toHaveTextContent("1 → 2 个模型");
    expect(tableRows[2]).toHaveTextContent("不新增未通过模型：gpt-6-luna");
    await user.click(d.getByRole("button", { name: "确认应用 2 个接入" }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes.find((w) => w.source === "a").targets[0].models).toEqual({
      "gpt-5.5": "gpt-5.5",
      "gpt-6-luna": "gpt-6-luna",
      "gpt-6-sol": "gpt-6-sol",
    });
    expect(writes.find((w) => w.source === "b").targets[0].models).toEqual({
      "gpt-5.5": "gpt-5.5",
      "gpt-6-sol": "gpt-6-sol",
    });
  },
);
