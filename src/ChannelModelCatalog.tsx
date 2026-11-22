import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Plus } from "lucide-react";
import { controlRequest } from "./api";
import { LoadingIndicator } from "./LoadingIndicator";
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
    retry: false,
    refetchOnWindowFocus: false,
  });
  const inventory = useChannelManagement(!member);
  const item = inventory.data?.data.find(m => m.source_id === source && m.provider === provider) || member;
  const [name, setName] = useState("");
  const [busy, setBusy] = useState<"refresh" | "add">();
  const [feedback, setFeedback] = useState("");
  const [error, setError] = useState("");

  async function update(action: "refresh" | "add") {
    const model = name.trim();
    if (busy || (action === "add" && !model)) return;
    setBusy(action);
    setFeedback("");
    setError("");
    try {
      await client.cancelQueries({ queryKey, exact: true });
      const next = await controlRequest<Catalog>(path, {
        method: "POST",
        body: JSON.stringify({ action, revision, ...(action === "add" ? { model } : {}) }),
      });
      client.setQueryData(queryKey, next);
      if (action === "add") setName("");
      setFeedback(action === "refresh"
        ? `已保存上游目录：本次返回 ${next.models.filter(m => m.observed).length} 个，未更改任何 Key 的接入。`
        : "已保存手工模型，未更改任何 Key 的接入。");
    } catch (error) {
      setError(error instanceof Error ? error.message : "模型目录保存失败");
    } finally {
      setBusy(undefined);
    }
  }

  return <>
    <section className="channel-catalog-toolbar" aria-label="上游模型目录">
      <p className="muted">刷新或手工添加只更新目录，不改变请求路由。新增接入前请先检测模型。</p>
      <div>
        <button type="button" className="button" disabled={!!busy} onClick={() => void update("refresh")}>
          <Download size={15} />{busy === "refresh" ? "正在读取…" : "从上游读取模型"}
        </button>
        <form onSubmit={e => { e.preventDefault(); void update("add"); }}>
          <input aria-label="手工添加上游模型 ID" placeholder="上游模型 ID" maxLength={256}
            value={name} onChange={e => setName(e.target.value)} disabled={!!busy} />
          <button className="button" disabled={!!busy || !name.trim()}><Plus size={15} />添加到目录</button>
        </form>
      </div>
      {catalog.isPending && <LoadingIndicator size="compact" label="读取已保存上游目录" />}
      {catalog.data?.refreshed_at && <small className="muted">上次刷新：{new Date(catalog.data.refreshed_at).toLocaleString()}</small>}
      {(error || catalog.error) && <p role="alert" className="negative">
        {error || catalog.error?.message}{" "}
        <button type="button" className="button small" disabled={!!busy} onClick={() => {
          setError(""); void onSettingsChanged().then(() => catalog.refetch());
        }}>重新读取配置</button>
      </p>}
      {feedback && <p role="status">{feedback}</p>}
    </section>
    {item ? <ConfiguredChannelDialog item={item} close={close} submitAdd={submitAdd} prices={prices}
      catalog={{ models: catalog.data?.models ?? [], ownerKey }} />
      : inventory.isPending ? <LoadingIndicator size="compact" label="读取渠道接入" />
      : <div role="alert" className="error-banner">
        {inventory.error?.message || "当前渠道的接入配置不可用。已保存的目录不会丢失。"}{" "}
        <button type="button" className="button small" onClick={() => void inventory.refetch()}>重新读取接入</button>
        {!!catalog.data?.models.length && <details>
          <summary>查看已保存目录（{catalog.data.models.length}）</summary>
          <ul>{catalog.data.models.map(({model,manual,observed}) => <li key={model}>{model}{manual ? " · 手工" : observed ? "" : " · 本次未返回"}</li>)}</ul>
        </details>}
      </div>}
  </>;
}
