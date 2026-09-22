import { useQuery } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import { ChannelSettings } from "./ChannelSettings";
import type { ManagedChannel } from "./channelManagement";

export function ChannelRoutes({ sourceId, providers }: { sourceId: string; providers: string[] }) {
  const query = useQuery({
    queryKey: ["channel-routes", sourceId],
    queryFn: ({ signal }) => controlRequest<{ data: { provider: string; model: string; api_key_id: string; key_prefix: string; key_position: number; position: number }[]; unavailable_keys: string[] }>(
      `/v1/sources/${encodeURIComponent(sourceId)}/channel-routes`, { signal }),
    staleTime: 5000, retry: false,
  });
  const rows = (query.data?.data || []).filter(row => providers.includes(row.provider));
  return <>
    {query.isPending && <p role="status"><Spinner small /> 正在读取路由位置…</p>}
    {(query.error || !!query.data?.unavailable_keys?.length) && <div className="error-banner" role="alert">
      {query.error?.message || "部分 API key 路由暂不可用，当前列表不完整。"}
      <button className="button small" onClick={() => void query.refetch()} disabled={query.isFetching}>重新读取路由</button>
    </div>}
    {rows.length > 0 && <div className="sub-route-table"><table><thead><tr><th>API key</th><th>渠道</th><th>模型</th><th>当前路由位置</th></tr></thead><tbody>
      {rows.map(row => <tr key={`${row.api_key_id}:${row.provider}:${row.model}`}><td>Key {row.key_position}<small className="check-source">{row.key_prefix}</small></td><td>{row.provider}</td><td>{row.model}</td><td>第 {row.position} 位</td></tr>)}
    </tbody></table></div>}
    {query.isSuccess && !query.data.unavailable_keys?.length && !rows.length && <p className="muted">尚未配置到任何 API key。</p>}
  </>;
}

export function ConfiguredChannelDialog({ item, close }: { item: ManagedChannel; close: () => void }) {
  return <Dialog.Root open onOpenChange={open => { if (!open) close(); }}><Dialog.Portal>
    <Dialog.Overlay className="dialog-overlay" />
    <Dialog.Content className="guide-dialog sub-import-dialog">
      <Dialog.Title>添加到渠道</Dialog.Title>
      <Dialog.Description>{item.name} · {item.source_name}</Dialog.Description>
      <Dialog.Close className="icon-button detail-close" aria-label="关闭添加渠道"><X size={18} /></Dialog.Close>
      <p className="muted">已配置模型：{item.models.join("、") || "暂无"}</p>
      <h3>当前 API key 路由</h3>
      <ChannelRoutes sourceId={item.source_id} providers={[item.provider]} />
      <div className="sub-installed-actions"><ChannelSettings row={{ provider: item.provider, provider_name: item.name, source_id: item.source_id, source_name: item.source_name, model: item.models[0] || "" }} /></div>
    </Dialog.Content>
  </Dialog.Portal></Dialog.Root>;
}
