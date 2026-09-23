import { useState } from "react";
import type { ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Trash2, X } from "lucide-react";
import { ChannelSettings } from "./ChannelSettings";
import { controlRequest } from "./api";
import { Spinner } from "./ui";

interface BindingTarget {
  source_id: string;
  source_name?: string;
  provider: string;
  name: string;
  model: string;
}
export function ChannelBindingActions({
  target,
  onEdit,
  editLabel,
  disabled,
  remove,
}: {
  target: BindingTarget;
  onEdit: () => void;
  editLabel?: string;
  disabled?: boolean;
  remove: ReactNode;
}) {
  return (
    <div className="sub-installed-actions binding-actions">
      <button
        type="button"
        className="button small"
        aria-label={editLabel}
        disabled={disabled}
        onClick={onEdit}
      >
        <Pencil size={13} />
        编辑
      </button>
      <ChannelSettings
        row={{
          provider: target.provider,
          provider_name: target.name,
          source_id: target.source_id,
          source_name: target.source_name,
          model: target.model,
        }}
      />
      {remove}
    </div>
  );
}

export function RemoveConfiguredBinding({
  target,
  keyId,
  keyPosition,
}: {
  target: BindingTarget;
  keyId: string;
  keyPosition: number;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <button type="button" className="button small">
          <Trash2 size={13} />
          删除
        </button>
      </Dialog.Trigger>
      {open && (
        <RemoveDialog
          target={target}
          keyId={keyId}
          keyPosition={keyPosition}
          close={() => setOpen(false)}
        />
      )}
    </Dialog.Root>
  );
}
function RemoveDialog({
  target,
  keyId,
  keyPosition,
  close,
}: {
  target: BindingTarget;
  keyId: string;
  keyPosition: number;
  close: () => void;
}) {
  const client = useQueryClient();
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const query = useQuery({
    queryKey: [
      "binding-remove-options",
      target.source_id,
      keyId,
      target.provider,
    ],
    queryFn: ({ signal }) =>
      controlRequest<{ revision: string; channels: { provider: string }[] }>(
        "/v1/sub2api/channel-options?" +
          new URLSearchParams({
            source_id: target.source_id,
            api_key_id: keyId,
          }),
        { signal },
      ),
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const exists = query.data?.channels.some(
    (c) => c.provider === target.provider,
  );
  async function remove() {
    if (busy || !query.data || !exists) return;
    setBusy(true);
    setError("");
    try {
      await controlRequest("/v1/channel-management", {
        method: "DELETE",
        signal: AbortSignal.timeout(60000),
        body: JSON.stringify({
          source_id: target.source_id,
          api_key_id: keyId,
          provider: target.provider,
          revision: query.data.revision,
        }),
      });
      await Promise.all(
        [
          "channel-management",
          "channel-routes",
          "sub2api-imports",
          "catalog",
          "control-catalog",
          "channel-controls",
          "sub-import-options",
          "configured-import-options",
        ].map((name) => client.invalidateQueries({ queryKey: [name] })),
      );
      close();
    } catch (e) {
      setError(e instanceof Error ? e.message : "删除失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <BindingRemoveDialog
      target={target}
      keyPosition={keyPosition}
      busy={busy}
      confirmDisabled={query.isFetching || query.isError || !exists}
      close={close}
      onConfirm={() => void remove()}
    >
      {query.isFetching && (
        <p role="status">
          <Spinner small />
          正在核对当前接入…
        </p>
      )}
      {(error || query.error) && (
        <div role="alert" className="error-banner">
          {error || query.error?.message}
          <button
            type="button"
            className="button small"
            disabled={busy || query.isFetching}
            onClick={() => {
              setError("");
              void query.refetch();
            }}
          >
            重新读取配置
          </button>
        </div>
      )}
      {query.isSuccess && !exists && (
        <p role="alert">该渠道已不在此 API key 中，请刷新后核对。</p>
      )}
    </BindingRemoveDialog>
  );
}

export function BindingRemoveDialog({
  target,
  keyPosition,
  busy,
  confirmDisabled = false,
  close,
  onConfirm,
  children,
}: {
  target: Pick<BindingTarget, "source_id" | "source_name" | "name">;
  keyPosition: number;
  busy: boolean;
  confirmDisabled?: boolean;
  close: () => void;
  onConfirm: () => void;
  children?: ReactNode;
}) {
  return (
    <Dialog.Portal>
      <Dialog.Overlay className="dialog-overlay route-edit-overlay" />
      <Dialog.Content
        className="guide-dialog route-edit-dialog binding-remove-dialog"
        onEscapeKeyDown={(e) => {
          if (busy) e.preventDefault();
        }}
        onPointerDownOutside={(e) => {
          if (busy) e.preventDefault();
        }}
      >
        <div className="binding-remove-heading">
          <span className="binding-remove-icon">
            <Trash2 size={20} />
          </span>
          <Dialog.Title>删除渠道接入</Dialog.Title>
        </div>
        <Dialog.Description>
          将从此 API key 移除以下渠道及其全部模型路由，其他 API key
          的接入不受影响。
        </Dialog.Description>
        <button
          className="icon-button detail-close"
          aria-label="关闭删除确认"
          disabled={busy}
          onClick={close}
        >
          <X size={18} />
        </button>
        <dl className="binding-remove-scope">
          <div>
            <dt>接入位置</dt>
            <dd>
              {target.source_name || target.source_id} · Key {keyPosition}
            </dd>
          </div>
          <div>
            <dt>渠道</dt>
            <dd>{target.name}</dd>
          </div>
        </dl>
        {children}
        <div className="binding-remove-actions">
          <button
            type="button"
            className="button"
            disabled={busy}
            onClick={close}
            autoFocus
          >
            取消
          </button>
          <button
            type="button"
            className="button danger"
            disabled={busy || confirmDisabled}
            onClick={onConfirm}
          >
            {busy && <Spinner small />}确认删除
          </button>
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  );
}
