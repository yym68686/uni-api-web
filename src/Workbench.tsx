import { useEffect, useId, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { ChevronDown, CircleAlert, X } from "lucide-react";
import { Tip } from "./ui";

const compactQuery = "(max-width: 650px)";
function subscribeCompact(listener: () => void) {
  const query = globalThis.matchMedia?.(compactQuery);
  query?.addEventListener("change", listener);
  return () => query?.removeEventListener("change", listener);
}
const compactSnapshot = () => !!globalThis.matchMedia?.(compactQuery).matches;
export const useCompactWorkbench = () => useSyncExternalStore(subscribeCompact, compactSnapshot, () => false);

export function useWorkbenchTools() {
  const [heading, observeHeading] = useState<HTMLDivElement | null>(null);
  const [level, setLevel] = useState(0);
  useEffect(() => {
    if (!heading) return;
    const update = () => {
      const width = heading.clientWidth;
      setLevel(width >= 1150 ? 3 : width >= 930 ? 2 : width >= 690 ? 1 : 0);
    };
    update();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(update);
    observer.observe(heading);
    return () => observer.disconnect();
  }, [heading]);
  return { observeHeading, level };
}

export function WorkbenchDisclosure({ label, children, icon, className = "", title }: {
  label: string;
  children: ReactNode;
  icon?: ReactNode;
  className?: string;
  title?: string;
}) {
  const id = useId();
  const disclosure = useRef<HTMLDetailsElement>(null);
  const insidePointer = useRef<PointerEvent | null>(null);
  useEffect(() => {
    const closeOutside = (event: PointerEvent) => {
      const panel = disclosure.current;
      if (panel?.open && insidePointer.current !== event && !event.composedPath().includes(panel)) panel.open = false;
    };
    document.addEventListener("pointerdown", closeOutside);
    return () => document.removeEventListener("pointerdown", closeOutside);
  }, []);
  return <details ref={disclosure} className={`workbench-disclosure ${className}`} onPointerDownCapture={event => {
    // React portal children are inside this disclosure even though they are outside its DOM.
    insidePointer.current = event.nativeEvent;
  }} onKeyDown={event => {
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

export function WorkbenchNotice({ messages, label = "数据状态" }: {
  messages: readonly (string | false | null | undefined)[];
  label?: string;
}) {
  const visible = [...new Set(messages.filter((message): message is string => !!message))];
  if (!visible.length) return null;
  return <span className="workbench-notice">
    <span className="sr-only" role="status">{visible.map(message => <span key={message}>{message}</span>)}</span>
    <Tip text={<div className="workbench-notice-text">{visible.map(message => <p key={message}>{message}</p>)}</div>}>
      <CircleAlert size={15} aria-label={label} />
    </Tip>
  </span>;
}
