import { useRef } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import { AccountForm, type SiteLoginDetails } from "./SiteAccountForm";
import { CompactDialogHeader } from "./CompactDialog";
import { LoadingIndicator } from "./LoadingIndicator";

export function BalanceRecoveryDialog({ id, user, requiresBrowser, close, saved }: {
  id: string;
  user: string;
  requiresBrowser: boolean;
  close: () => void;
  saved: () => void;
}) {
  const opener = useRef(document.activeElement);
  const account = useQuery({
    queryKey: ["site-login-details", user, id],
    queryFn: ({ signal }) => controlRequest<SiteLoginDetails>(
      `/v1/sub2api/accounts/${encodeURIComponent(id)}/login`, { signal },
    ),
    retry: false,
  });
  if (account.isSuccess && account.data.id === id) {
    return <AccountForm initial={account.data} sessionOnly requiresBrowser={requiresBrowser} close={close} saved={saved} returnFocusTo={opener.current} />;
  }
  return <Dialog.Root open onOpenChange={open => { if (!open) close(); }}>
    <Dialog.Portal>
      <Dialog.Overlay className="dialog-overlay" />
      <Dialog.Content className="guide-dialog compact-dialog" aria-describedby={undefined}
        onCloseAutoFocus={event => {
          event.preventDefault();
          if (opener.current instanceof HTMLElement && opener.current.isConnected) opener.current.focus();
        }}>
        <CompactDialogHeader>重新登录站点账号</CompactDialogHeader>
        <div className="compact-dialog-scroll">
          {account.isFetching ? <LoadingIndicator label="读取绑定账号" /> :
            <p role="alert" className="error-banner">站点账号信息暂不可用，请重新读取。</p>}
        </div>
        {!account.isFetching && <div className="compact-dialog-footer">
          <button type="button" className="button small" onClick={() => void account.refetch()}>重新读取账号</button>
        </div>}
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
