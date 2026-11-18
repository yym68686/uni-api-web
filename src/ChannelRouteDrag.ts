import { useEffect, useRef, type PointerEvent } from "react";
import type { Channel } from "./types";

type Destination = { row: Channel; after: boolean; element: HTMLElement };
type Drag = {
  pointer: number;
  handle: HTMLButtonElement;
  from: Channel;
  origin: HTMLElement;
  startX: number;
  startY: number;
  x: number;
  y: number;
  started: boolean;
  target?: Destination;
  frame?: number;
};

export function useChannelRouteDrag(
  scope: string,
  rowsById: ReadonlyMap<string, Channel>,
  move: (from: Channel, to: Channel, after: boolean) => void,
) {
  const active = useRef<Drag | null>(null);

  function clear() {
    const drag = active.current;
    if (!drag) return;
    active.current = null;
    if (drag.frame !== undefined) cancelAnimationFrame(drag.frame);
    delete drag.origin.dataset.routeDragging;
    if (drag.target) delete drag.target.element.dataset.routeDrop;
    if (drag.handle.hasPointerCapture?.(drag.pointer)) drag.handle.releasePointerCapture(drag.pointer);
  }

  useEffect(() => {
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && active.current) {
        event.preventDefault();
        clear();
      }
    };
    window.addEventListener("keydown", escape);
    window.addEventListener("blur", clear);
    return () => {
      clear();
      window.removeEventListener("keydown", escape);
      window.removeEventListener("blur", clear);
    };
  }, [scope]);

  function updateTarget(drag: Drag) {
    const hit = document.elementFromPoint(drag.x, drag.y)?.closest<HTMLElement>("[data-route-row]");
    const row = hit?.dataset.routeRow && rowsById.get(hit.dataset.routeRow);
    const target = hit && row && row.source_id === drag.from.source_id && row.provider !== drag.from.provider
      ? { row, element: hit }
      : undefined;
    const rect = target?.element.getBoundingClientRect();
    const after = !!rect && drag.y >= rect.top + rect.height / 2;
    if (drag.target?.element === target?.element && (!target || drag.target?.after === after)) return;
    if (drag.target) delete drag.target.element.dataset.routeDrop;
    drag.target = undefined;
    if (target) {
      target.element.dataset.routeDrop = after ? "after" : "before";
      drag.target = { ...target, after };
    }
  }

  function scrollOffset(y: number) {
    if (y < 0 || y > window.innerHeight) return 0;
    return y > window.innerHeight - 60 ? 12 : y < 80 ? -12 : 0;
  }

  function autoScroll(drag: Drag) {
    drag.frame = undefined;
    const offset = scrollOffset(drag.y);
    if (!offset) return;
    const before = window.scrollY;
    window.scrollBy(0, offset);
    if (before === window.scrollY) return;
    updateTarget(drag);
    drag.frame = requestAnimationFrame(() => autoScroll(drag));
  }

  return (row: Channel) => ({
    onPointerDown(event: PointerEvent<HTMLButtonElement>) {
      if (!event.isPrimary || event.button !== 0 || active.current) return;
      const handle = event.currentTarget;
      const origin = handle.closest<HTMLElement>("[data-route-row]");
      if (!origin) return;
      handle.setPointerCapture?.(event.pointerId);
      active.current = {
        pointer: event.pointerId, handle, origin, from: row, started: false,
        startX: event.clientX, startY: event.clientY, x: event.clientX, y: event.clientY,
      };
    },
    onPointerMove(event: PointerEvent<HTMLButtonElement>) {
      const drag = active.current;
      if (!drag || drag.pointer !== event.pointerId) return;
      drag.x = event.clientX;
      drag.y = event.clientY;
      if (!drag.started) {
        if (Math.hypot(drag.x - drag.startX, drag.y - drag.startY) < 5) return;
        drag.started = true;
        drag.origin.dataset.routeDragging = "true";
      }
      updateTarget(drag);
      if (drag.frame === undefined && scrollOffset(drag.y)) {
        drag.frame = requestAnimationFrame(() => autoScroll(drag));
      }
    },
    onPointerUp(event: PointerEvent<HTMLButtonElement>) {
      const drag = active.current;
      if (!drag || drag.pointer !== event.pointerId) return;
      if (drag.started) {
        drag.x = event.clientX;
        drag.y = event.clientY;
        updateTarget(drag);
      }
      const target = drag.target;
      clear();
      if (target) move(drag.from, target.row, target.after);
    },
    onPointerCancel(event: PointerEvent<HTMLButtonElement>) {
      if (active.current?.pointer === event.pointerId) clear();
    },
    onLostPointerCapture(event: PointerEvent<HTMLButtonElement>) {
      if (active.current?.pointer === event.pointerId) clear();
    },
  });
}
