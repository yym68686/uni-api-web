import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { useSubAccounts } from "./sub2apiAccounts";
import { SiteAccountSettings } from "./SiteAccountSettings";
import { Spinner } from "./ui";
import { CompactDialogHeader } from "./CompactDialog";

export default function BalanceWalletDialog({id,close}:{id:string;close:()=>void}) {
  const accounts=useSubAccounts(true,true);
  const client=useQueryClient();
  const account=accounts.data?.data.find(item=>item.id===id);
  if (account && !accounts.isError) return <SiteAccountSettings account={account} initialSection="recharge" close={close} saved={()=>{
    void client.invalidateQueries({queryKey:["sub2api"]});
    void client.invalidateQueries({queryKey:["sub2api-headers"]});
    void client.invalidateQueries({queryKey:["sub2api-imports"]});
  }}/>;
  return <Dialog.Root open onOpenChange={open=>{if(!open)close();}}>
    <Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="guide-dialog compact-dialog" aria-describedby={undefined}>
      <CompactDialogHeader>站点账号充值</CompactDialogHeader>
      <div className="compact-dialog-scroll">
        {accounts.isPending ? <p role="status"><Spinner small/> 读取绑定账号…</p> :
          <p role="alert">{accounts.error?.message || "此绑定账号不存在或不可用。"}</p>}
      </div>
      {!accounts.isPending && <div className="compact-dialog-footer">
        <button className="button" onClick={()=>void accounts.refetch()}>重新读取账号</button>
      </div>}
    </Dialog.Content></Dialog.Portal>
  </Dialog.Root>;
}
