import type { ManagementPage } from "./managementQuery";

export const managementPageMediaType =
  "application/vnd.uni-api.management-page-v2+json";

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value);
const invalid = (): never => {
  throw Error("渠道分页数据不完整，请重新读取");
};

// v1 remains accepted during independent frontend/backend deployment. v2 is
// lossless transport deduplication, not a partial summary or a retained cache.
export function decodeManagementPage(input: unknown): ManagementPage {
  if (!record(input)) return invalid();
  if (input.format !== "management-page-v2")
    return input as unknown as ManagementPage;
  if (!Array.isArray(input.results) || !Array.isArray(input.rows))
    return invalid();
  const results: unknown[] = input.results;
  for (const value of results) {
    if (
      !record(value) ||
      !record(value.availability) ||
      !record(value.quality) ||
      typeof value.model !== "string" ||
      typeof value.verdict !== "string" ||
      typeof value.checked_at !== "number" ||
      !Number.isFinite(value.checked_at)
    )
      return invalid();
  }
  const reference = (index: unknown) => {
    if (index === null) return null;
    if (
      typeof index !== "number" ||
      !Number.isSafeInteger(index) ||
      index < 0 ||
      index >= results.length
    )
      return invalid();
    // Original JSON produced independent objects at each occurrence. Preserve
    // that isolation so editing a copy cannot change a sibling check or row.
    return structuredClone(results[index]);
  };
  const resultObject = (value: unknown): Record<string, unknown> => {
    if (
      !record(value) ||
      !Object.hasOwn(value, "result_ref") ||
      Object.hasOwn(value, "result")
    )
      return invalid();
    const { result_ref, ...rest } = value;
    return { ...rest, result: reference(result_ref) };
  };
  const rows = input.rows.map((value) => {
    if (!record(value) || !record(value.target) || !Array.isArray(value.checks))
      return invalid();
    const target = resultObject(value.target);
    if (value.target.models !== null && !Array.isArray(value.target.models))
      return invalid();
    target.models =
      value.target.models === null
        ? null
        : value.target.models.map(resultObject);
    const row: Record<string, unknown> = {
      ...value,
      target,
      checks: value.checks.map(resultObject),
    };
    if (Object.hasOwn(value, "selected"))
      row.selected = resultObject(value.selected);
    return row;
  });
  const { results: _results, rows: _rows, format: _format, ...page } = input;
  return {
    ...page,
    format: "management-page-v1",
    rows,
  } as unknown as ManagementPage;
}
