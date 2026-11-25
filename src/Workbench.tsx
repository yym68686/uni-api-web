import { useCallback, useEffect, useId, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { ChevronDown, CircleAlert, X } from "./icons";
import { Tip } from "./ui";

export function useViewportQuery(query: string) {
  const subscribe = useCallback((listener: () => void) => {
    const media = globalThis.matchMedia?.(query);
    media?.addEventListener("change", listener);
    return () => media?.removeEventListener("change", listener);
  }, [query]);
  const snapshot = useCallback(() => !!globalThis.matchMedia?.(query)?.matches, [query]);
  return useSyncExternalStore(subscribe, snapshot, () => false);
}
export const useCompactWorkbench = () => useViewportQuery("(max-width: 650px)");

export function ResponsiveActions({ children, label = "更多", width = 1100 }: {
  children: ReactNode; label?: string; width?: number;
}) {
  const compact = useViewportQuery(`(max-width: ${width}px)`);
  return compact ? <WorkbenchDisclosure label={label} className="secondary-actions">{children}</WorkbenchDisclosure> : <>{children}</>;
}

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
    let frame: number | null = null;
    const observer = new ResizeObserver(() => {
      if (frame !== null) return;
      // Moving controls changes the heading's size. Commit after observer delivery.
      frame = requestAnimationFrame(() => {
        frame = null;
        update();
      });
    });
    observer.observe(heading);
    return () => {
      observer.disconnect();
      if (frame !== null) cancelAnimationFrame(frame);
    };
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
  const body = useRef<HTMLDivElement>(null);
  const insidePointer = useRef<PointerEvent | null>(null);
  useEffect(() => {
    const closeOutside = (event: PointerEvent) => {
      const panel = disclosure.current;
      if (panel?.open && insidePointer.current !== event && !event.composedPath().includes(panel)) panel.open = false;
    };
    const position = () => {
      const anchor = disclosure.current?.querySelector("summary")?.getBoundingClientRect();
      const popover = body.current;
      if (!disclosure.current?.open || !anchor || !popover || window.innerWidth <= 780) return;
      const width = popover.offsetWidth;
      const height = popover.offsetHeight;
      popover.style.setProperty("--workbench-left", Math.max(12, Math.min(anchor.right - width, innerWidth - width - 12)) + "px");
      const top = anchor.bottom + height + 20 <= innerHeight || anchor.top < height + 20
        ? Math.min(anchor.bottom + 8, innerHeight - height - 12) : anchor.top - height - 8;
      popover.style.setProperty("--workbench-top", Math.max(12, top) + "px");
    };
    const details = disclosure.current;
    details?.addEventListener("toggle", position);
    document.addEventListener("pointerdown", closeOutside);
    window.addEventListener("resize", position);
    window.addEventListener("scroll", position, true);
    return () => {
      details?.removeEventListener("toggle", position);
      document.removeEventListener("pointerdown", closeOutside);
      window.removeEventListener("resize", position);
      window.removeEventListener("scroll", position, true);
    };
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
    <div ref={body} id={id} className="workbench-popover">
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

export function WorkbenchNotice({ messages, label = "数据状态", urgent = false }: {
  messages: readonly (string | false | null | undefined)[];
  label?: string;
  urgent?: boolean;
}) {
  const visible = [...new Set(messages.filter((message): message is string => !!message))];
  if (!visible.length) return null;
  return <span className="workbench-notice">
    <span className="sr-only" role={urgent ? "alert" : "status"}>{visible.map(message => <span key={message}>{message}</span>)}</span>
    <Tip text={<div className="workbench-notice-text">{visible.map(message => <p key={message}>{message}</p>)}</div>}>
      <CircleAlert size={15} aria-label={label} />
    </Tip>
  </span>;
}
