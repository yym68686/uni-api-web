import { vi } from "vitest";
import { managementRows } from "../src/channelManagement";
import { filterManagementRows } from "../src/managementQueryFilters";
import { managementJoinState } from "../src/channelOptimizationPlan";
import { sharedFingerprintIndex } from "../src/fingerprintResults";
import {
  rowFingerprints,
  fingerprintStatus,
  FINGERPRINT_MODELS,
} from "../src/ModelFingerprints";
import { SUB_MODELS } from "../src/sub2apiModels";
import type { SubFilters } from "../src/sub2apiPreferences";
import type {
  ManagementRow,
  ManagedChannel,
  ConfiguredCheck,
} from "../src/channelManagement";
import type { SubAccount } from "../src/Sub2apiChecks";
import type { SubImports } from "../src/sub2apiImports";
import type { Routes } from "../src/channelRouteData";
import type { FingerprintRecord } from "../src/ModelFingerprints";
import type { ModelPrice } from "../src/types";

// Adapts the existing synthetic interaction fixtures to the new wire contract.
// The old browser predicates are also the independent Go contract oracle. These
// reads call only the test's mocked fetch, never a live server. Separate wire
// tests assert that the actual component sends only one paged request on entry.
const installed = Symbol("management-page-fixture");
export function installManagementPageFixture() {
  const fixtureFetch = globalThis.fetch as typeof fetch & {
    [installed]?: boolean;
  };
  if (fixtureFetch[installed]) return;
  const wrapped = Object.assign(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), location.origin);
      if (!url.pathname.endsWith("/channel-management/query"))
        return fixtureFetch(input, init);
      const root =
        url.origin + url.pathname.replace(/\/channel-management\/query$/, "");
      const read = async <T>(suffix: string): Promise<T> => {
        const response = await fixtureFetch(root + suffix, {
          signal: init?.signal,
        });
        if (!response.ok) throw Error(await response.text());
        return response.json();
      };
      const [a, c, i, k, f, p] = await Promise.all([
        read<{ data: SubAccount[] }>("/sub2api/accounts"),
        read<{ data: ManagedChannel[] }>("/channel-management"),
        read<SubImports>("/sub2api/channels"),
        read<{ data: ConfiguredCheck[] }>("/channel-management/checks"),
        read<{ data: FingerprintRecord[] }>("/model-fingerprints"),
        read<{ data: ModelPrice[] }>("/prices"),
      ]);
      // One older fixture deliberately models summary-v1 transport; reconstruct
      // its synthetic account state before applying the independent predicates.
      const legacy = a as unknown as {
        format?: string;
        data: {
          value: {
            account?: SubAccount;
            account_id?: string;
            target?: SubAccount["targets"][number];
          };
        }[];
      };
      const accounts =
        legacy.format === "summary-v1"
          ? legacy.data.flatMap((row) =>
              row.value.account
                ? [
                    {
                      ...row.value.account,
                      targets: legacy.data.flatMap((r) =>
                        r.value.account_id === row.value.account!.id &&
                        r.value.target
                          ? [{ ...r.value.target, details_omitted: true }]
                          : [],
                      ),
                    },
                  ]
                : [],
            )
          : a.data || [];
      const channels = (c.data || []).filter((c) => c.provider && c.source_id),
        imports = (i.data || []).filter((c) => c.provider && c.source_id),
        checks = (k.data || []).filter((c) => c.source_id && c.model),
        prices = (p.data || []).filter((p) => p.model);
      const sourceIDs = [
        ...new Set([...channels, ...imports].map((item) => item.source_id)),
      ];
      const routes = Object.fromEntries(
        await Promise.all(
          sourceIDs.map(async (id) => [
            id,
            await read<Routes>(`/sources/${id}/channel-routes`),
          ]),
        ),
      );
      const filters = JSON.parse(
        url.searchParams.get("filters")!,
      ) as SubFilters;
      const index = sharedFingerprintIndex(
        f.data || [],
        channels,
        [],
        imports,
        [],
      );
      const full = managementRows(
        accounts,
        channels,
        imports,
        filters.model,
        filters.accountId,
        checks,
      );
      const filtered = filterManagementRows(full, filters, {
        prices,
        fingerprintReady: true,
        fingerprints: (row) =>
          rowFingerprints(row, index, filters.model).map((x) =>
            fingerprintStatus(x.record),
          ),
        joined: (row) => managementJoinState(row, i, routes),
      });
      const rows = filtered.rows;
      const eligible: ManagementRow[] = [],
        seen = new Set<string>();
      for (const row of rows) {
        if (
          !(row.account.id === "" && row.configured) &&
          !(
            row.target.active &&
            row.target.key_id > 0 &&
            !["queued", "running"].includes(row.account.state) &&
            row.target.state !== "error"
          )
        )
          continue;
        const id = row.account.id
          ? `${row.account.id}:${row.target.group_id}`
          : `configured:${row.configured!.provider}:${row.configured!.engine}`;
        if (!seen.has(id)) {
          seen.add(id);
          eligible.push(row);
        }
      }
      const revision = "fixture-current",
        read_at_ms = Date.now();
      if (url.searchParams.has("selection"))
        return Response.json({
          format: "management-selection-v1",
          revision,
          read_at_ms,
          rows,
          eligible,
          total: rows.length,
          eligible_count: eligible.length,
          input: {
            accounts,
            channels,
            imports,
            checks,
            routes,
            unavailable_sources: [],
            unavailable_imports: [],
          },
          prices,
        });
      const offset = Number(url.searchParams.get("cursor") || 0),
        page = rows.slice(offset, offset + 25);
      return Response.json({
        format: "management-page-v1",
        revision,
        read_at_ms,
        rows: page,
        total: rows.length,
        eligible_count: eligible.length,
        offset,
        page_size: 25,
        next_cursor:
          offset + 25 < rows.length ? String(offset + 25) : undefined,
        previous_cursor: offset ? String(offset - 25) : undefined,
        selection_token: "fixture-selection",
        accounts: accounts.map((account) => ({
          ...account,
          targets: [],
          active_groups: account.targets.filter((t) => t.active).length,
          completed_groups: account.targets.filter(
            (t) => t.active && t.state === "done",
          ).length,
        })),
        rates: filtered.rates,
        platforms: filtered.platforms,
        models: [
          ...new Set([
            ...SUB_MODELS,
            filters.model,
            ...channels.flatMap((c) => c.models),
            ...checks.map((c) => c.model),
            ...accounts.flatMap((a) =>
              a.targets.flatMap((t) => (t.models || []).map((c) => c.model)),
            ),
          ]),
        ].filter(Boolean),
        detection_models: [
          ...SUB_MODELS,
          ...[...new Set(rows.flatMap((row) => row.checks.map((c) => c.model)))]
            .filter((m) => !SUB_MODELS.includes(m))
            .sort(),
        ],
        context: {
          prices,
          imports,
          routes,
          fingerprints: Object.fromEntries(
            page.map((row) => [
              row.id,
              rowFingerprints(row, index, filters.model),
            ]),
          ),
          fingerprint_models: FINGERPRINT_MODELS,
        },
      });
    },
    { [installed]: true },
  );
  vi.stubGlobal("fetch", wrapped);
}
