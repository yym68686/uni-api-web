import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import { dateTime } from "./format";
interface Persistence {
  enabled: boolean;
  status: string;
  message: string;
  rules: number;
  channels: number;
  saved_at: number | null;
  restored_at: number | null;
}
export function ControlPersistence({
  source,
  name,
  lazyLoad = false,
}: {
  source: string;
  name: string;
  lazyLoad?: boolean;
}) {
  const [expanded, setExpanded] = useState(!lazyLoad);
  const path = `/v1/sources/${encodeURIComponent(source)}/control-persistence`;
  const query = useQuery({
    queryKey: ["control-persistence", source],
    queryFn: ({ signal }) => controlRequest<Persistence>(path, { signal }),
    enabled: expanded,
    staleTime:30_000,
    refetchInterval: entry => expanded && ["pending","restoring"].includes(entry.state.data?.status || "") ? 5000 : false,
    refetchIntervalInBackground:false,
    retry: false,
  });
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  async function toggle() {
    if (busy || !query.data) return;
    setBusy(true);
    setError("");
    try {
      await controlRequest(path, {
        method: "PUT",
        body: JSON.stringify({ enabled: !query.data.enabled }),
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : "设置保存失败");
    } finally {
      await query.refetch();
      setBusy(false);
    }
  }
  const data = query.data;
  if (!expanded) return <button className="button small" onClick={()=>setExpanded(true)}>保留临时配置设置</button>;
  if (data?.status === "managed") return <small>临时配置由「完整配置托管」统一保存。</small>;
  return (
    <div className="control-persistence">
      <label>
        <button
          type="button"
          role="switch"
          className={`switch ${data?.enabled !== false ? "on" : ""}`}
          aria-checked={data?.enabled !== false}
          aria-label={`${name} 保留临时配置`}
          disabled={busy || !data}
          onClick={() => void toggle()}
        >
          <span />
        </button>
        保留临时配置
      </label>
      <small>
        {data?.enabled === false
          ? "关闭后，来源下次重启将读取原配置文件。"
          : data?.status === "saved"
            ? `已保存 ${data.channels} 个临时渠道 · ${data.rules} 条规则${data.restored_at ? ` · 最近恢复 ${dateTime(data.restored_at)}` : ""}`
            : data?.status === "restoring"
              ? "正在恢复重启前的配置…"
              : data?.status === "error"
                ? data.message
                : "正在保存当前配置…"}
      </small>
      {(error || query.error) && (
        <small role="alert" className="negative">
          {error || query.error?.message}
        </small>
      )}
      {lazyLoad && <div className="recharge-actions">
        <button className="button small" disabled={query.isFetching || busy} onClick={() => void query.refetch()}>读取状态</button>
        <button className="button small ghost" disabled={busy} onClick={() => setExpanded(false)}>收起</button>
      </div>}
    </div>
  );
}
