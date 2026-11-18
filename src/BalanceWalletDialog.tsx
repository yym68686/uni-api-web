import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { useSubAccounts } from "./sub2apiAccounts";
import { SiteAccountSettings } from "./SiteAccountSettings";
import { Spinner } from "./ui";

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
    <Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="guide-dialog">
      <Dialog.Title>站点账号充值</Dialog.Title>
      <Dialog.Description>核对绑定账号后，才能查看充值配置或订单。</Dialog.Description>
      {accounts.isPending ? <p role="status"><Spinner small/> 读取绑定账号</p> :
        <p role="alert">{accounts.error?.message || "此绑定账号不存在，或不再可用。"}</p>}
      <div className="recharge-actions">
        {!accounts.isPending && <button className="button" onClick={()=>void accounts.refetch()}>重新读取账号</button>}
        <Dialog.Close className="button">关闭</Dialog.Close>
      </div>
    </Dialog.Content></Dialog.Portal>
  </Dialog.Root>;
}
