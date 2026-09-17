import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, RefreshCw, X } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import type { ConsoleSource } from "./SourceSettings";
import type { KeyInfo } from "./types";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import { modelChecks, importModelLabel } from "./sub2apiResults";
interface Options {
  provider?: string;
  supported: boolean;
  revision: string;
  keys: KeyInfo[];
  channels: { provider: string; model: string }[];
}
export function Sub2apiImport({
  account,
  target,
  close,
}: {
  account: SubAccount;
  target: SubTarget;
  close: () => void;
}) {
  const client = useQueryClient();
  const checks = useMemo(() => modelChecks(target), [target]);
  const available = useMemo(
    () =>
      checks
        .filter(
          (c) =>
            c.state === "done" && c.result?.availability.status === "success",
        )
        .map((c) => c.model),
    [checks],
  );
  const [models, setModels] = useState<string[]>(available),
    [source, setSource] = useState(""),
    [key, setKey] = useState(""),
    [position, setPosition] = useState(1),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [success, setSuccess] = useState("");
  const sources = useQuery({
    queryKey: ["sub-import-sources"],
    queryFn: ({ signal }) =>
      controlRequest<{ data: ConsoleSource[] }>("/v1/sources", { signal }),
    retry: false,
  });
  const options = useQuery({
    queryKey: ["sub-import-options", source, key, account.id, target.group_id],
    queryFn: ({ signal }) =>
      controlRequest<Options>(
        "/v1/sub2api/channel-options?" +
          new URLSearchParams({
            source_id: source,
            api_key_id: key,
            account_id: account.id,
            group_id: String(target.group_id),
          }),
        { signal },
      ),
    enabled: !!source,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const positions = useMemo(() => {
    if (!models.length) return 1;
    return Math.min(
      ...models.map(
        (model) =>
          new Set(
            options.data?.channels
              .filter(
                (c) =>
                  c.model === model && c.provider !== options.data?.provider,
              )
              .map((c) => c.provider) || [],
          ).size + 1,
      ),
    );
  }, [models, options.data]);
  const validPosition = Math.min(position, positions);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const result = await controlRequest<{ message: string }>(
        "/v1/sub2api/channels",
        {
          method: "POST",
          signal: AbortSignal.timeout(60000),
          body: JSON.stringify({
            account_id: account.id,
            group_id: target.group_id,
            source_id: source,
            api_key_id: key,
            models,
            position: validPosition,
            revision: options.data?.revision,
          }),
        },
      );
      setSuccess(result.message);
      await Promise.all([
        client.invalidateQueries({ queryKey: ["catalog"] }),
        client.invalidateQueries({ queryKey: ["channel-controls"] }),
        client.invalidateQueries({ queryKey: ["control-catalog"] }),
        client.invalidateQueries({ queryKey: ["sub2api"] }),
      ]);
    } catch (e) {
      setError(e instanceof Error ? e.message : "添加失败");
      void options.refetch();
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open && !busy) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content
          className="guide-dialog sub-import-dialog"
          onEscapeKeyDown={(e) => {
            if (busy) e.preventDefault();
          }}
          onPointerDownOutside={(e) => {
            if (busy) e.preventDefault();
          }}
        >
          <Dialog.Title>添加到渠道</Dialog.Title>
          <Dialog.Description>
            {account.name} / {target.name}
          </Dialog.Description>
          <button
            className="icon-button detail-close"
            aria-label="关闭添加渠道"
            disabled={busy}
            onClick={close}
          >
            <X size={18} />
          </button>
          {success ? (
            <div role="status" className="sub-import-success">
              <p>{success}</p>
              <button className="button primary" onClick={close}>
                完成
              </button>
            </div>
          ) : (
            <form onSubmit={submit}>
              <fieldset disabled={busy}>
                <legend>模型</legend>
                <div className="sub-model-options">
                  {checks.map((check) => {
                    const model = check.model;
                    return (
                      <label key={model}>
                        <input
                          type="checkbox"
                          checked={models.includes(model)}
                          disabled={!available.includes(model)}
                          onChange={(e) =>
                            setModels((old) =>
                              e.target.checked
                                ? [...old, model]
                                : old.filter((m) => m !== model),
                            )
                          }
                        />
                        <span>{model}</span>
                        {!available.includes(model) && (
                          <small>{importModelLabel(check)}</small>
                        )}
                      </label>
                    );
                  })}
                </div>
              </fieldset>
              <label className="sub-import-field">
                uni-api 来源
                <select
                  aria-label="添加到 uni-api 来源"
                  value={source}
                  disabled={busy}
                  required
                  onChange={(e) => {
                    setSource(e.target.value);
                    setKey("");
                    setPosition(1);
                    setError("");
                  }}
                >
                  <option value="">选择来源</option>
                  {sources.data?.data.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="sub-import-field">
                API key
                <select
                  aria-label="添加到 API key"
                  value={key}
                  required
                  disabled={
                    !source ||
                    options.isFetching ||
                    busy ||
                    !options.data?.supported
                  }
                  onChange={(e) => {
                    setKey(e.target.value);
                    setPosition(1);
                  }}
                >
                  <option value="">选择 API key</option>
                  {options.data?.keys.map((k) => (
                    <option key={k.key_id} value={k.key_id}>
                      Key {k.position} · {k.prefix}
                    </option>
                  ))}
                </select>
              </label>
              <label className="sub-import-field">
                添加位置
                <select
                  aria-label="渠道添加位置"
                  value={validPosition}
                  disabled={!key || options.isFetching || busy}
                  onChange={(e) => setPosition(Number(e.target.value))}
                >
                  {Array.from({ length: positions }, (_, i) => (
                    <option key={i} value={i + 1}>
                      第 {i + 1} 位{i === 0 ? " · 优先请求" : ""}
                    </option>
                  ))}
                </select>
              </label>
              <p className="sub-import-note">
                仅对所选 key 和模型生效，uni-api
                重启后清除。添加时会创建或复用独立业务
                key，使用上游默认额度，不占用检测 key 的 $1 额度。
              </p>
              {source && options.data && !options.data.supported && (
                <div role="alert" className="error-banner">
                  来源尚不支持临时添加渠道，请更新 uni-api。
                </div>
              )}
              {(error || sources.error || options.error) && (
                <div role="alert" className="error-banner">
                  {error || sources.error?.message || options.error?.message}
                  <button
                    type="button"
                    className="button small"
                    onClick={() => void options.refetch()}
                  >
                    <RefreshCw size={13} />
                    刷新选项
                  </button>
                </div>
              )}
              <div className="sub-import-actions">
                <button
                  className="button"
                  type="button"
                  disabled={busy}
                  onClick={close}
                >
                  取消
                </button>
                <button
                  className="button primary"
                  disabled={
                    busy ||
                    !source ||
                    !key ||
                    !models.length ||
                    !options.data?.supported ||
                    options.isFetching ||
                    options.isError
                  }
                >
                  {busy ? <Spinner small /> : <Plus size={15} />}添加到渠道
                </button>
              </div>
            </form>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
