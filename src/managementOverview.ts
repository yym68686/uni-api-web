export const managementColumnsStorageKey = "uni-console:management-columns:v1";

const overviewColumns = new Set(["model", "platform", "rate", "availability", "latency", "fingerprint", "checked", "batch"]);

export function managementOverviewForColumns(model: string, columns?: unknown): boolean {
  return !model && (columns == null || Array.isArray(columns) && columns.every(c => overviewColumns.has(c)));
}

export function managementBootstrapOverview(model: string): boolean {
  try {
    return managementOverviewForColumns(model, JSON.parse(localStorage.getItem(managementColumnsStorageKey) || "null"));
  } catch {
    return managementOverviewForColumns(model);
  }
}
