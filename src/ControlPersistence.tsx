import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import { time } from "./format";
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
}: {
  source: string;
  name: string;
}) {
  const path = `/v1/sources/${encodeURIComponent(source)}/control-persistence`;
  const query = useQuery({
    queryKey: ["control-persistence", source],
    queryFn: ({ signal }) => controlRequest<Persistence>(path, { signal }),
    refetchInterval: 5000,
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
            ? `已保存 ${data.channels} 个临时渠道 · ${data.rules} 条规则${data.restored_at ? ` · 最近恢复 ${time(data.restored_at)}` : ""}`
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
    </div>
  );
}
