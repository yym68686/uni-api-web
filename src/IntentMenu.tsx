
import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";
import { ChevronRight, Ellipsis } from "./icons";
import "./intentMenu.css";

type Point = { x: number; y: number };
type Rect = Pick<DOMRect, "left" | "right" | "top" | "bottom">;
type Action = { label: string; run: () => void; disabled?: boolean; danger?: boolean };
type Item = Action | { label: string; children: Action[] };
const openDelay = 140;
const closeDelay = 240;
const graceLimit = 480;

export function inMenuTriangle(origin: Point, pointer: Point, panel: Rect, left: boolean) {
  const edge = left ? panel.right : panel.left;
  const dx = edge - origin.x;
  if (!dx) return false;
  const t = (pointer.x - origin.x) / dx;
  if (t < 0 || t > 1) return false;
  return pointer.y >= origin.y + t * (panel.top - origin.y)
    && pointer.y <= origin.y + t * (panel.bottom - origin.y);
}

function menuButtons(list: HTMLElement | null) {
  return Array.from(list?.querySelectorAll<HTMLButtonElement>(":scope > li > button:not(:disabled)") ?? []);
}

export function IntentMenu({ label, context, items }: { label: string; context: string; items: Item[] }) {
  const id = useId();
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const child = useRef<HTMLUListElement>(null);
  const origin = useRef<Point | null>(null);
  const deadline = useRef(0);
  const pending = useRef<ReturnType<typeof setTimeout> | null>(null);
  const waiting = useRef<{ index: number; guarded: boolean } | null>(null);
  const lastPointer = useRef<Point | null>(null);
  const [open, setOpen] = useState(false);
  const [branch, setBranch] = useState(-1);
  const [side, setSide] = useState<"left" | "right">("right");
  const [touch, setTouch] = useState(false);
  const requestedFocus = useRef<"first" | "last" | "child" | null>(null);
  function cancel() {
    if (pending.current != null) clearTimeout(pending.current);
    pending.current = null;
    waiting.current = null;
  }
  function close(focus: boolean) {
    cancel();
    setOpen(false);
    setBranch(-1);
    if (focus) trigger.current?.focus();
  }
  useEffect(() => cancel, []);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: globalThis.PointerEvent) => {
      if (!event.composedPath().includes(root.current!)) close(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [open]);
  useLayoutEffect(() => {
    if (!open) return;
    function place() {
      const anchor = trigger.current?.getBoundingClientRect();
      const parent = panel.current;
      if (!anchor || !parent) return;
      if (!window.matchMedia("(max-width: 780px)").matches) {
        const width = parent.offsetWidth;
        parent.style.left = Math.max(12, Math.min(anchor.right - width, innerWidth - width - 12)) + "px";
        const height = parent.offsetHeight;
        parent.style.top = Math.max(12, anchor.bottom + height + 20 <= innerHeight ? anchor.bottom + 8 : anchor.top - height - 8) + "px";
        if (child.current) {
          const childWidth = child.current.offsetWidth;
          const rect = parent.getBoundingClientRect();
          const left = rect.right + childWidth + 12 > innerWidth && rect.left - childWidth >= 12;
          setSide(left ? "left" : "right");
          const row = menuButtons(list.current).find(button => button.dataset.index === String(branch));
          const top = row?.getBoundingClientRect().top ?? rect.top;
          child.current.style.top = Math.max(12, Math.min(top, innerHeight - child.current.offsetHeight - 12)) - rect.top + "px";
        }
      }
    }
    place();
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [open, branch, touch]);
  useLayoutEffect(() => {
    if (!open || !requestedFocus.current) return;
    const direction = requestedFocus.current;
    requestedFocus.current = null;
    const buttons = menuButtons(direction === "child" ? child.current : list.current);
    (direction === "last" ? buttons.at(-1) : buttons[0])?.focus();
  }, [open, branch]);
  function selectBranch(index: number, focus = false) {
    cancel();
    if (focus && index === branch) {
      menuButtons(child.current)[0]?.focus();
      return;
    }
    if (focus) requestedFocus.current = "child";
    origin.current = index !== -1 && !focus ? lastPointer.current : null;
    deadline.current = 0;
    setBranch(index);
  }
  function schedule(index: number, delay: number, guarded = false) {
    if (waiting.current?.index === index && waiting.current.guarded === guarded) return;
    cancel();
    waiting.current = { index, guarded };
    pending.current = setTimeout(() => selectBranch(index), Math.max(0, delay));
  }
  function parentMove(event: ReactPointerEvent, index: number, hasChild: boolean) {
    if (event.pointerType !== "mouse" || touch) return;
    lastPointer.current = { x: event.clientX, y: event.clientY };
    if (index === branch) {
      cancel();
      origin.current = lastPointer.current;
      deadline.current = 0;
      return;
    }
    const next = hasChild ? index : -1;
    const pointer = lastPointer.current;
    const rect = child.current?.getBoundingClientRect();
    if (origin.current && rect && inMenuTriangle(origin.current, pointer, rect, side === "left")) {
      if (!deadline.current) deadline.current = performance.now() + graceLimit;
      if (performance.now() < deadline.current) {
        schedule(next, deadline.current - performance.now(), true);
        return;
      }
    }
    schedule(next, next < 0 ? closeDelay : openDelay);
  }
  function keyboard(event: KeyboardEvent<HTMLUListElement>, submenu: boolean) {
    if (event.target !== event.currentTarget && !event.currentTarget.contains(event.target as Node)) return;
    const buttons = menuButtons(event.currentTarget);
    const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      const index = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 :
        (current + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length;
      buttons[index]?.focus();
    } else if (submenu && (event.key === "ArrowLeft" || event.key === "Escape")) {
      cancel();
      setBranch(-1);
      menuButtons(list.current).find(button => button.dataset.index === String(branch))?.focus();
    } else if (!submenu && event.key === "ArrowRight") {
      const index = Number(buttons[current]?.dataset.index);
      if (items[index] && "children" in items[index]) selectBranch(index, true);
      else return;
    } else if (!submenu && event.key === "Escape") close(true);
    else return;
    event.preventDefault();
    event.stopPropagation();
  }
  function action(item: Action) {
    close(true);
    item.run();
  }
  const selected = items[branch];
  return <div className="intent-menu" ref={root} onBlur={event => {
    if (event.relatedTarget instanceof Node && !event.currentTarget.contains(event.relatedTarget)) close(false);
  }}>
    <button type="button" className="button small intent-menu-trigger" ref={trigger}
      aria-label={label} aria-haspopup="menu" aria-expanded={open} aria-controls={open ? id : undefined}
      onPointerDown={event => setTouch(event.pointerType !== "mouse")}
      onClick={() => {
        if (open) close(false);
        else { requestedFocus.current = "first"; setOpen(true); }
      }} onKeyDown={event => {
        if (event.key === "Escape" && open) {
          event.preventDefault(); close(true);
        } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
          event.preventDefault();
          setTouch(false);
          if (open) {
            const buttons = menuButtons(list.current);
            (event.key === "ArrowUp" ? buttons.at(-1) : buttons[0])?.focus();
          } else {
            requestedFocus.current = event.key === "ArrowUp" ? "last" : "first";
            setOpen(true);
          }
        }
      }}>
      <Ellipsis /> <span>更多</span>
    </button>
    {open && <div ref={panel} className="intent-menu-panel" data-side={side} data-touch={touch || undefined}
      onPointerLeave={event => { if (event.pointerType === "mouse" && !touch) schedule(-1, closeDelay); }}>
      <strong className="intent-menu-context" id={id + "-context"}>{context}</strong>
      <ul id={id} ref={list} role="menu" aria-labelledby={id + "-context"} onKeyDown={event => keyboard(event, false)}>
        {items.map((item, index) => <li role="none" key={item.label}>
          {"children" in item ? <button type="button" role="menuitem" tabIndex={-1} data-index={index}
            aria-haspopup="menu" aria-expanded={branch === index} aria-controls={branch === index ? id + "-child" : undefined}
            onPointerEnter={event => parentMove(event, index, true)}
            onPointerMove={event => parentMove(event, index, true)}
            onClick={() => selectBranch(index, true)}>{item.label}<ChevronRight /></button>
          : <button type="button" role="menuitem" tabIndex={-1} data-index={index} data-danger={item.danger || undefined}
              disabled={item.disabled} onPointerEnter={event => parentMove(event, index, false)} onPointerMove={event => parentMove(event, index, false)} onClick={() => action(item)}>{item.label}</button>}
        </li>)}
      </ul>
      {selected && "children" in selected && <ul id={id + "-child"} ref={child} className="intent-menu-child"
        role="menu" aria-label={selected.label} onKeyDown={event => keyboard(event, true)}
        onPointerEnter={cancel} onPointerLeave={event => { if (event.pointerType === "mouse" && !touch) schedule(-1, closeDelay); }}>
        {selected.children.map(item => <li role="none" key={item.label}><button type="button" role="menuitem" tabIndex={-1}
          disabled={item.disabled} onClick={() => action(item)}>{item.label}</button></li>)}
      </ul>}
    </div>}
  </div>;
}
