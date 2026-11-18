import { useState, type ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { WorkbenchTabs } from "./Workbench";
import "./detailDrawer.css";

type DrawerPage = { id: string; label: string; content: ReactNode };

export function DetailDrawer({
  title, context, actions, pages, close, closeLabel, className = "",
}: {
  title: ReactNode; context: string; actions?: ReactNode; pages: DrawerPage[];
  close: () => void; closeLabel: string; className?: string;
}) {
  const [visited, setVisited] = useState(() => [pages[0].id]);
  const current = visited.at(-1);
  const open = pages.some(page => page.id === current) ? current : pages[0].id;
  return (
    <Dialog.Root open onOpenChange={next => { if (!next) close(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className={`detail-panel workbench-drawer ${className}`}>
          <header className="drawer-header">
            <div>
              <Dialog.Title>{title}</Dialog.Title>
              <Dialog.Description title={context}>{context}</Dialog.Description>
            </div>
            <Dialog.Close className="icon-button" aria-label={closeLabel}><X size={18} /></Dialog.Close>
            {actions && <div className="drawer-actions">{actions}</div>}
          </header>
          <WorkbenchTabs label="详情分区" tabs={pages.map(({ id, label }) => [id, label])}
            value={open!} onChange={id => setVisited(old => [...old.filter(value => value !== id), id])} />
          {pages.filter(page => page.id === open || visited.includes(page.id)).map(page =>
            <div key={page.id} className="drawer-body" hidden={page.id !== open}>{page.content}</div>)}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
