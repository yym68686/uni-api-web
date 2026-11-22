import { DetailLoading } from "./PageLoading";
import { lazy, Suspense, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Settings2, SlidersHorizontal, X } from "lucide-react";
import type { Channel, ModelPrice } from "./types";
import type { ManagedChannel } from "./channelManagement";
import type { ConfiguredChannelAddDraft, SubmitChannelAdd } from "./channelAdd";
const Editor = lazy(() => import("./ChannelSettingsEditor").then(m=>({default:m.Editor})));

export type SettingsChannel = Pick<Channel, "provider" | "provider_name" | "source_id" | "source_name" | "model">;
export const GLOBAL_SETTINGS_SCOPE = "__uni_console_global_preferences__";
type ChannelConfigurationProps = {
  row: SettingsChannel;
  member?: ManagedChannel;
  prices?: ModelPrice[];
  initialModelTab?: boolean;
  submitAdd?: SubmitChannelAdd<ConfiguredChannelAddDraft>;
};

export function ChannelSettings({ triggerLabel, ...props }: ChannelConfigurationProps & { triggerLabel?: string }) {
  const [open, setOpen] = useState(false);
  return <ChannelConfigurationDialog {...props} open={open} onOpenChange={setOpen} trigger={
    <button className="button channel-settings-trigger">
      <Settings2 size={16} />
      {triggerLabel || (props.row.provider === GLOBAL_SETTINGS_SCOPE ? "全局设置" : "渠道设置")}
    </button>
  } />;
}

export function ChannelConfigurationDialog({ row, member, prices, initialModelTab, submitAdd, open, onOpenChange, trigger }: ChannelConfigurationProps & {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  trigger?: React.ReactNode;
}) {
  const title = row.provider === GLOBAL_SETTINGS_SCOPE ? "全局设置" : "渠道设置";
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      {trigger && <Dialog.Trigger asChild>{trigger}</Dialog.Trigger>}
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay channel-settings-overlay" />
        <Dialog.Content className="channel-settings-dialog">
          <header className="settings-dialog-header">
            <div className="settings-title-icon">
              <SlidersHorizontal size={21} />
            </div>
            <div className="settings-title-copy">
              <Dialog.Title>{title}</Dialog.Title>
              <Dialog.Description title={row.provider}>
                <strong>{row.provider_name || row.provider}</strong>
                <span>·</span>
                {row.source_name || row.source_id}
              </Dialog.Description>
            </div>
            <Dialog.Close
              className="settings-icon-button settings-close"
              aria-label={`关闭${title}`}
            >
              <X size={20} />
            </Dialog.Close>
          </header>
          {open && <Suspense fallback={<DetailLoading kind="form" label="渠道设置" />}>
            <Editor row={row} member={member} prices={prices} initialModelTab={initialModelTab} submitAdd={submitAdd} onClose={() => onOpenChange(false)} />
          </Suspense>}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
