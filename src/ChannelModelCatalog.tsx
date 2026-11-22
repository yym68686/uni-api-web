import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCw, X } from "lucide-react";
import { controlRequest } from "./api";
import { channelReadRecovery } from "./channelReadRecovery";
import { LoadingIndicator } from "./LoadingIndicator";
import { Spinner } from "./ui";
import { ConfiguredChannelDialog } from "./ChannelRoutes";
import { useChannelManagement } from "./channelManagement";
import type { ManagedChannel } from "./channelManagement";
import type { ConfiguredChannelAddDraft, SubmitChannelAdd } from "./channelAdd";
import type { ModelPrice } from "./types";

export type CatalogModel = { model: string; manual?: boolean; observed?: boolean };
type Catalog = { models: CatalogModel[]; refreshed_at: string | null };

export function ChannelModelCatalog({ source, provider, revision, ownerKey, member, prices, close, onSettingsChanged, submitAdd }: {
  source: string;
  provider: string;
  revision: string;
  ownerKey?: string;
  member?: ManagedChannel;
  prices?: ModelPrice[];
  close: () => void;
  onSettingsChanged: () => Promise<void>;
  submitAdd?: SubmitChannelAdd<ConfiguredChannelAddDraft>;
}) {
  const client = useQueryClient();
  const path = `/v1/sources/${encodeURIComponent(source)}/channel-settings/model-catalog?` + new URLSearchParams({ provider });
  const queryKey = ["channel-model-catalog", source, provider];
  const catalog = useQuery({
    queryKey,
    queryFn: ({ signal }) => controlRequest<Catalog>(path, { signal }),
    ...channelReadRecovery,
    refetchOnWindowFocus: false,
  });
  const inventory = useChannelManagement(!member);
  const item = inventory.data?.data.find(m => m.source_id === source && m.provider === provider) || member;
  const [name, setName] = useState("");
  const [busy, setBusy] = useState<"refresh" | "add">();
  const [manualOpen, setManualOpen] = useState(false);
  const [error, setError] = useState("");

  async function update(action: "refresh" | "add") {
    const model = name.trim();
    if (busy || (action === "add" && !model)) return;
    setBusy(action);
    setError("");
    try {
      await client.cancelQueries({ queryKey, exact: true });
      const next = await controlRequest<Catalog>(path, {
        method: "POST",
        body: JSON.stringify({ action, revision, ...(action === "add" ? { model } : {}) }),
      });
      client.setQueryData(queryKey, next);
      if (action === "add") {
        setName("");
        setManualOpen(false);
      }
    } catch (error) {
      setError(error instanceof Error ? error.message : "模型目录保存失败");
    } finally {
      setBusy(undefined);
    }
  }

  const actions = <>
    <button type="button" className="button" aria-label="从上游读取模型" title="从上游读取模型"
      disabled={!!busy} aria-busy={busy === "refresh"} onClick={() => void update("refresh")}>
      {busy === "refresh" ? <Spinner small /> : <RefreshCw size={15} />}<span>读取上游</span>
    </button>
    <button type="button" className="button" aria-expanded={manualOpen} disabled={!!busy} onClick={() => setManualOpen(!manualOpen)}>
      <Plus size={15} /><span>手工添加</span>
    </button>
  </>;
  const extra = manualOpen && <form className="channel-catalog-add" onSubmit={e => { e.preventDefault(); void update("add"); }}>
    <input aria-label="手工添加上游模型 ID" placeholder="上游模型 ID" maxLength={256}
      value={name} onChange={e => setName(e.target.value)} disabled={!!busy} />
    <button className="button" aria-busy={busy === "add"} disabled={!!busy || !name.trim()}>添加到目录</button>
    <button className="settings-icon-button" type="button" aria-label="取消手工添加" disabled={!!busy} onClick={() => setManualOpen(false)}><X size={16} /></button>
  </form>;
  const feedback = error && <div role="alert" className="channel-model-error">
        {error}{" "}
        <button type="button" className="button small" disabled={!!busy} onClick={() => {
          setError(""); void onSettingsChanged();
        }}>重新读取配置</button>
      </div>;
  if (item) return <ConfiguredChannelDialog item={item} close={close} submitAdd={submitAdd} prices={prices}
    catalog={{ models: catalog.data?.models ?? [], ownerKey, actions, extra, feedback, pending: catalog.isPending,
      read: { error: catalog.error, isFetching: catalog.isFetching || !!busy, refetch: catalog.refetch } }} />;
  return <div className="settings-body">
    <div className="channel-catalog-actions">{actions}</div>{extra}{feedback}
    {catalog.error && <div role="alert" className="channel-model-error">
      {catalog.error.message}
      <button type="button" className="button small" disabled={catalog.isFetching || !!busy}
        onClick={() => void catalog.refetch({ cancelRefetch: false })}>重新读取目录</button>
    </div>}
    {inventory.isPending ? <LoadingIndicator size="compact" label="读取渠道接入" /> : <>
      <div role="alert" className="channel-model-error">
        {inventory.error?.message || "渠道接入不可用。"}{" "}
        <button type="button" className="button small" onClick={() => void inventory.refetch()}>重新读取接入</button>
      </div>
      {!!catalog.data?.models.length && <details>
        <summary>已保存目录（{catalog.data.models.length}）</summary>
        <ul>{catalog.data.models.map(({model}) => <li key={model}>{model}</li>)}</ul>
      </details>}
    </>}
  </div>;
}
