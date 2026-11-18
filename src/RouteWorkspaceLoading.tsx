import * as Dialog from "@radix-ui/react-dialog";
import { CompactDialogHeader } from "./CompactDialog";
import { Spinner } from "./ui";

export function RouteWorkspaceLoading({ context, label, close, nested, error, retry }: {
  context: string; label: string; close: () => void; nested?: boolean; error?: string; retry?: () => void;
}) {
  return <Dialog.Root open onOpenChange={open => { if (!open) close(); }}>
    <Dialog.Portal>
      <Dialog.Overlay className={`dialog-overlay${nested ? " route-edit-overlay" : ""}`} />
      <Dialog.Content className={`guide-dialog compact-dialog channel-import-workspace${nested ? " route-edit-dialog" : ""}`}
        onCloseAutoFocus={event => event.preventDefault()}>
        <CompactDialogHeader context={context} closeLabel="关闭模型编辑">添加到渠道</CompactDialogHeader>
        <div className="compact-dialog-scroll route-loading-body">
          {error ? <div role="alert" className="error-banner">{error}
            {retry && <button type="button" className="button small" onClick={retry}>重试</button>}
          </div> : <p role="status"><Spinner small /> {label}</p>}
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
