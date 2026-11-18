import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, controlRequest } from "./api";
import { channelMembers } from "./channelManagement";
import type { ManagementRow } from "./channelManagement";

export interface DetectionTarget {
  source_id?: string;
  provider?: string;
  account_id?: string;
  group_id?: number;
}
export const detectionKey = (t: DetectionTarget) =>
  JSON.stringify([
    t.source_id || "",
    t.provider || "",
    t.account_id || "",
    t.group_id || 0,
  ]);
export function detectionTargets(
  row: Pick<ManagementRow, "account" | "target" | "configured">,
): DetectionTarget[] {
  if (row.account.id)
    return [{ account_id: row.account.id, group_id: row.target.group_id }];
  return row.configured
    ? channelMembers(row.configured).map((m) => ({
        source_id: m.source_id,
        provider: m.provider,
      }))
    : [];
}
export const detectionEnabled = (
  row: Pick<ManagementRow, "account" | "target" | "configured">,
  disabled: Set<string>,
) => detectionTargets(row).every((t) => !disabled.has(detectionKey(t)));
export function useChannelDetection(owner: string, enabled = true) {
  const client = useQueryClient(),
    key = ["channel-detection-settings", owner];
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) =>
      controlRequest<{ disabled?: DetectionTarget[] }>(
        "/v1/channel-management/detection-settings",
        { signal },
      ),
    enabled,
    retry: false,
    staleTime: 15000,
  });
  const busyRef = useRef(new Set<string>()),
    [saving, setSaving] = useState(new Set<string>()),
    [error, setError] = useState("");
  const scopedKey = (target: DetectionTarget) => JSON.stringify([owner,detectionKey(target)]);
  const isSaving = (row: Parameters<typeof detectionTargets>[0]) =>
    detectionTargets(row).some(target => saving.has(scopedKey(target)));
  const disabled = new Set((query.data?.disabled || []).map(detectionKey));
  async function save(
    row: Pick<ManagementRow, "account" | "target" | "configured">,
    enabled: boolean,
  ) {
    const targets = detectionTargets(row);
    const keys = targets.map(scopedKey);
    if (!keys.length || keys.some(key => busyRef.current.has(key))) return;
    keys.forEach(key => busyRef.current.add(key));
    setSaving(new Set(busyRef.current));
    setError("");
    try {
      await controlRequest("/v1/channel-management/detection-settings", {
        method: "PATCH",
        body: JSON.stringify({ targets, enabled }),
      });
      client.setQueryData<{ disabled?: DetectionTarget[] }>(key, (old) => ({
        disabled: [
          ...(old?.disabled || []).filter(
            (t) => !targets.some((x) => detectionKey(x) === detectionKey(t)),
          ),
          ...(enabled ? [] : targets),
        ],
      }));
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 404
          ? "检测开关服务待更新，请稍后重试。"
          : e instanceof Error
            ? e.message
            : "检测开关保存失败",
      );
    } finally {
      keys.forEach(key => busyRef.current.delete(key));
      setSaving(new Set(busyRef.current));
    }
  }
  return { ...query, disabled, busy: saving.size > 0, isSaving, error, save };
}
