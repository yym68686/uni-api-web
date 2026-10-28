// Browser-local, out-of-band metadata: no payload fields, URLs, owners,
// filters, credentials, persistence or telemetry requests are retained.
export interface ManagementReadTrace {
  readonly requestId: string;
  readonly validationAcknowledged: boolean;
  readonly started: number;
  readonly headers: number;
  readonly decoded: number;
}

const reads = new WeakMap<object, ManagementReadTrace>();

export function rememberManagementReadTrace(value: unknown, trace: ManagementReadTrace) {
  if (value !== null && typeof value === "object" && !Array.isArray(value))
    reads.set(value, trace);
}

export function managementReadTrace(value: unknown): ManagementReadTrace | undefined {
  return value !== null && typeof value === "object" ? reads.get(value) : undefined;
}
