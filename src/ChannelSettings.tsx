import { lazy, Suspense, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Settings2, SlidersHorizontal, X } from "lucide-react";
import type { Channel } from "./types";
import { Spinner } from "./ui";
const Editor = lazy(() => import("./ChannelSettingsEditor").then(m=>({default:m.Editor})));

export type SettingsChannel = Pick<Channel, "provider" | "provider_name" | "source_id" | "source_name" | "model">;
export const GLOBAL_SETTINGS_SCOPE = "__uni_console_global_preferences__";
export function ChannelSettings({ row }: { row: SettingsChannel }) {
  const title = row.provider === GLOBAL_SETTINGS_SCOPE ? "全局设置" : "渠道设置";
  const [open, setOpen] = useState(false);
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <button className="button channel-settings-trigger">
          <Settings2 size={16} />
          {title}
        </button>
      </Dialog.Trigger>
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
          {open && <Suspense fallback={<div role="status"><Spinner />正在读取渠道设置…</div>}><Editor row={row} onClose={() => setOpen(false)} /></Suspense>}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
