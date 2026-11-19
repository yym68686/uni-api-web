let dashboard: Promise<typeof import("./Dashboard")> | undefined;
let management: Promise<typeof import("./Sub2apiChecks")> | undefined;

export function loadDashboardModule() {
  return dashboard ??= import("./Dashboard").catch(error => {
    dashboard = undefined;
    throw error;
  });
}

export function loadManagementModule() {
  return management ??= import("./Sub2apiChecks").catch(error => {
    management = undefined;
    throw error;
  });
}

export function preloadManagementWorkspace() {
  // Request both chunks while the independently authorized page request is
  // in flight. Speculation must not surface as a separate UI error.
  void loadDashboardModule().catch(() => undefined);
  void loadManagementModule().catch(() => undefined);
}
