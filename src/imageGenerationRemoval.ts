type ObjectValue = Record<string, unknown>;

// Keep these rules equivalent to uni-api's structured __remove__ configuration.
const rules = [
  { path: "tools", where: { type: "image_generation" }, drop_empty: true },
  {
    path: "tool_choice",
    where_any: [
      "image_generation",
      { type: "image_generation" },
      { "tool.type": "image_generation" },
      { "function.name": "image_generation" },
    ],
  },
];

function object(value: unknown): ObjectValue | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as ObjectValue)
    : undefined;
}

function normalized(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(normalized);
  const record = object(value);
  if (!record) return value;
  return Object.fromEntries(
    Object.entries(record)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, item]) => [
        key,
        key === "where_any" && Array.isArray(item)
          ? item
              .map(normalized)
              .sort((a, b) =>
                JSON.stringify(a).localeCompare(JSON.stringify(b)),
              )
          : normalized(item),
      ]),
  );
}

function matches(value: unknown, rule: unknown) {
  return JSON.stringify(normalized(value)) === JSON.stringify(normalized(rule));
}

function removals(value: unknown): unknown[] {
  const remove = object(value)?.__remove__;
  return remove === undefined ? [] : Array.isArray(remove) ? remove : [remove];
}

export function removesImageGeneration(value: unknown): boolean {
  const existing = removals(value);
  return rules.every((rule) => existing.some((item) => matches(item, rule)));
}

export function setImageGenerationRemoval(
  value: unknown,
  enabled: boolean,
): ObjectValue | undefined {
  const next = structuredClone(object(value) ?? {});
  const existing = removals(next);
  const remove = enabled
    ? [
        ...existing,
        ...structuredClone(
          rules.filter((rule) => !existing.some((item) => matches(item, rule))),
        ),
      ]
    : existing.filter((item) => !rules.some((rule) => matches(item, rule)));
  if (remove.length) next.__remove__ = remove;
  else delete next.__remove__;
  return Object.keys(next).length ? next : undefined;
}
