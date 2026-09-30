import { expect, it, vi } from "vitest";
import { defaultFilters } from "./preferences";
import {
  applyScopedChannelSort,
  channelSortScope,
  defaultChannelSort,
  deleteSortTemplate,
  loadChannelSortPreferences,
  saveChannelSortPreferences,
  scopedChannelSort,
} from "./channelSortPreferences";
import type { ChannelSortPreferences } from "./channelSortPreferences";

const quality = [{ field: "quality", direction: "asc" }] as const;
const price = [{ field: "multiplier", direction: "asc" }] as const;
const base = "https://sort.example";
const filters = {
  ...defaultFilters,
  model: "gpt-6-astra",
  keyId: "opaque-key",
};

it("migrates old shared sorting into only the last visible scope, including all models", () => {
  for (const model of ["gpt-6-astra", ""]) {
    const legacy = {
      ...filters,
      model,
      sort: "custom",
      sortRules: JSON.stringify(quality),
    };
    const store = loadChannelSortPreferences(base, legacy);
    expect(scopedChannelSort(store, channelSortScope(legacy)).rules).toEqual(
      quality,
    );
    expect(
      scopedChannelSort(
        store,
        channelSortScope({ ...legacy, model: "gpt-6-luna" }),
      ),
    ).toEqual(defaultChannelSort());
    expect(
      scopedChannelSort(
        store,
        channelSortScope({ ...legacy, keyId: "another-key" }),
      ),
    ).toEqual(defaultChannelSort());
  }
});

it("isolates every filter combination, restores model templates after reload and leaves other services alone", () => {
  let store: ChannelSortPreferences = {
    scopes: {},
    templates: [
      { id: "quality", name: "质量优先", rules: [...quality] },
      { id: "price", name: "低价优先", rules: [...price] },
    ],
    modelTemplates: {},
  };
  store = applyScopedChannelSort(
    store,
    channelSortScope(filters),
    filters.model,
    { sort: "custom", rules: [...quality], templateId: "quality" },
  );
  const cheaper = { ...filters, model: "gpt-6-luna" };
  store = applyScopedChannelSort(
    store,
    channelSortScope(cheaper),
    cheaper.model,
    { sort: "custom", rules: [...price], templateId: "price" },
  );
  for (const [field, value] of Object.entries({
    model: "",
    sourceId: "other",
    keyId: "other",
    window: "24h",
    endpoint: "/v1/messages",
    stream: "true",
    statusFilter: "eligible",
    balanceFilter: "low",
    search: "site",
  }))
    expect(
      scopedChannelSort(
        store,
        channelSortScope({ ...filters, [field]: value }),
      ),
    ).toEqual(defaultChannelSort());
  expect(scopedChannelSort(store, channelSortScope(filters)).rules).toEqual(
    quality,
  );
  expect(scopedChannelSort(store, channelSortScope(cheaper)).rules).toEqual(
    price,
  );
  saveChannelSortPreferences(base, store);
  expect(loadChannelSortPreferences(base, defaultFilters)).toEqual(store);
  expect(store.modelTemplates).toEqual({
    "gpt-6-astra": "quality",
    "gpt-6-luna": "price",
  });
  expect(loadChannelSortPreferences("other", defaultFilters).templates).toEqual(
    [],
  );
});

it("deleting templates detaches references while retaining applied rules and renaming keeps selection identity", () => {
  let store: ChannelSortPreferences = {
    scopes: {},
    templates: [{ id: "t", name: "旧名称", rules: [...quality] }],
    modelTemplates: {},
  };
  store = applyScopedChannelSort(
    store,
    channelSortScope(filters),
    filters.model,
    { sort: "custom", rules: [...quality], templateId: "t" },
  );
  store.templates = store.templates.map((t) => ({ ...t, name: "新名称" }));
  saveChannelSortPreferences(base, store);
  expect(loadChannelSortPreferences(base, filters).templates[0].name).toBe(
    "新名称",
  );
  const removed = deleteSortTemplate(store, "t");
  expect(removed.templates).toEqual([]);
  expect(removed.modelTemplates).toEqual({});
  expect(scopedChannelSort(removed, channelSortScope(filters))).toEqual({
    sort: "custom",
    rules: quality,
  });
});

it("cleans corrupt saved entries and tolerates unavailable storage", () => {
  localStorage.setItem(
    `uni-console-channel-sorts:v1:${base}`,
    JSON.stringify({
      scopes: { bad: {} },
      templates: [
        null,
        {
          id: "t",
          name: "valid",
          rules: [{ field: "bogus", direction: "asc" }],
        },
      ],
      modelTemplates: { astra: "missing" },
    }),
  );
  expect(loadChannelSortPreferences(base, filters)).toEqual({
    scopes: {},
    templates: [{ id: "t", name: "valid", rules: [] }],
    modelTemplates: {},
  });
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw Error("blocked");
  });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw Error("blocked");
  });
  expect(() =>
    saveChannelSortPreferences(base, loadChannelSortPreferences(base, filters)),
  ).not.toThrow();
});
