import * as Dialog from "@radix-ui/react-dialog";
import { CompactDialogHeader } from "./CompactDialog";
import { LoadingIndicator } from "./LoadingIndicator";

export function RouteWorkspaceLoading({ context, label, close, nested, error, retry, title = "添加到渠道", closeLabel = "关闭模型编辑" }: {
  context: string; label: string; close: () => void; nested?: boolean; error?: string; retry?: () => void; title?: string; closeLabel?: string;
}) {
  return <Dialog.Root open onOpenChange={open => { if (!open) close(); }}>
    <Dialog.Portal>
      <Dialog.Overlay className={`dialog-overlay${nested ? " route-edit-overlay" : ""}`} />
      <Dialog.Content className={`guide-dialog compact-dialog channel-import-workspace${nested ? " route-edit-dialog" : ""}`}
        onCloseAutoFocus={event => event.preventDefault()}>
        <CompactDialogHeader context={context} closeLabel={closeLabel}>{title}</CompactDialogHeader>
        {error ? <div className="compact-dialog-scroll route-loading-body">
          <div role="alert" className="error-banner">{error}
            {retry && <button type="button" className="button small" onClick={retry}>重试</button>}
          </div>
        </div> : <LoadingIndicator size="fill" className="compact-dialog-scroll route-loading-body" label={label} />}
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
