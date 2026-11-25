import { useRef, type ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "./icons";

export function CompactDialogHeader({ children, context, closeLabel = "关闭", busy }: {
  children: ReactNode;
  context?: ReactNode;
  closeLabel?: string;
  busy?: boolean;
}) {
  return <header className="compact-dialog-header">
    <div>
      <Dialog.Title title={typeof children === "string" ? children : undefined}>{children}</Dialog.Title>
      {context && <Dialog.Description title={typeof context === "string" ? context : undefined}>{context}</Dialog.Description>}
    </div>
    <Dialog.Close type="button" className="icon-button" aria-label={closeLabel} disabled={busy}>
      <X size={16} aria-hidden="true" />
    </Dialog.Close>
  </header>;
}

export function RemovalDialog({ title, context, children, busy, error, close, confirm }: {
  title: string;
  context: string;
  children: ReactNode;
  busy: boolean;
  error?: string;
  close: () => void;
  confirm: () => void;
}) {
  const opener = useRef(document.activeElement);
  return <Dialog.Root open onOpenChange={open => { if (!open && !busy) close(); }}>
    <Dialog.Portal>
      <Dialog.Overlay className="dialog-overlay" />
      <Dialog.Content className="guide-dialog compact-dialog"
        onEscapeKeyDown={event => { if (busy) event.preventDefault(); }}
        onPointerDownOutside={event => { if (busy) event.preventDefault(); }}
        onCloseAutoFocus={event => {
          event.preventDefault();
          if (opener.current instanceof HTMLElement && opener.current.isConnected) opener.current.focus();
        }}>
        <CompactDialogHeader context={context} busy={busy}>{title}</CompactDialogHeader>
        <div className="compact-dialog-scroll">
          <p className="dialog-inline-note">{children}</p>
          {error && <p className="error-banner" role="alert">{error}</p>}
        </div>
        <div className="compact-dialog-footer">
          <Dialog.Close className="button small" disabled={busy}>取消</Dialog.Close>
          <button type="button" className="button small danger" disabled={busy} onClick={confirm}>{busy ? "移除中…" : "确认移除"}</button>
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
