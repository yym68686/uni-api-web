import type { useChannelImportKeys } from "./channelImportKeys";

// A failed refresh is not a failed selection. Keep an existing directory
// usable, but distinguish it from a cold read failure or revoked permission.
export function ChannelImportKeyFeedback({
  directory,
}: {
  directory?: ReturnType<typeof useChannelImportKeys>[number];
}) {
  if (!directory || (!directory.error && !directory.data?.refresh_failed))
    return null;
  const cached = !!directory.data;
  return (
    <div
      role={cached ? "status" : "alert"}
      className={cached ? "sub-import-note" : "error-banner"}
    >
      {cached
        ? "暂时无法更新 API key，已保留上次列表；保存前会核对最新配置。"
        : directory.error?.message || "API key 列表读取失败，请重试。"}
      <button
        type="button"
        className="button small"
        disabled={directory.isFetching}
        onClick={() => void directory.refetch()}
      >
        {directory.isFetching ? "正在更新 API key…" : "重新读取 API key"}
      </button>
    </div>
  );
}
