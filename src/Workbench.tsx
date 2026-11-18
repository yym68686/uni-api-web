import { useId, useSyncExternalStore, type ReactNode } from "react";
import { ChevronDown, X } from "lucide-react";

const compactQuery = "(max-width: 650px)";
function subscribeCompact(listener: () => void) {
  const query = globalThis.matchMedia?.(compactQuery);
  query?.addEventListener("change", listener);
  return () => query?.removeEventListener("change", listener);
}
const compactSnapshot = () => !!globalThis.matchMedia?.(compactQuery).matches;
export const useCompactWorkbench = () => useSyncExternalStore(subscribeCompact, compactSnapshot, () => false);

export function WorkbenchDisclosure({ label, children, icon, className = "", title }: {
  label: string;
  children: ReactNode;
  icon?: ReactNode;
  className?: string;
  title?: string;
}) {
  const id = useId();
  return <details className={`workbench-disclosure ${className}`} onKeyDown={event => {
    if (event.key !== "Escape" || event.defaultPrevented || !event.currentTarget.open || event.target instanceof HTMLElement && !event.currentTarget.contains(event.target)) return;
    event.preventDefault();
    event.currentTarget.open = false;
    event.currentTarget.querySelector("summary")?.focus();
  }}>
    <summary className="button small" aria-controls={id} title={title}>{icon}{label}<ChevronDown size={12} aria-hidden="true" /></summary>
    <div id={id} className="workbench-popover">
      <div className="workbench-popover-heading"><strong>{label}</strong><button type="button" className="icon-button" aria-label={`关闭${label}`} onClick={event => {
        const panel = event.currentTarget.closest("details");
        if (panel) {
          panel.open = false;
          panel.querySelector("summary")?.focus();
        }
      }}><X size={16} /></button></div>
      {children}
    </div>
  </details>;
}

export function WorkbenchTabs({ label, value, tabs, onChange }: {
  label: string;
  value: string;
  tabs: readonly (readonly [string, string])[];
  onChange: (value: string) => void;
}) {
  return <nav className="workbench-tabs" aria-label={label}>{tabs.map(([key, text]) =>
    <button key={key} type="button" aria-current={key === value ? "true" : undefined} onClick={() => onChange(key)}>{text}</button>)}</nav>;
}
