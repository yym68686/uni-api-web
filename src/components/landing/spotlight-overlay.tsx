"use client";

import * as React from "react";

interface SpotlightOverlayProps {
  targetId: string;
}

function isReducedMotion() {
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function supportsSpotlight() {
  return window.matchMedia("(pointer: fine)").matches;
}

export function SpotlightOverlay({ targetId }: SpotlightOverlayProps) {
  React.useEffect(() => {
    const el = document.getElementById(targetId);
    if (!el) return;
    if (isReducedMotion() || !supportsSpotlight()) return;

    el.style.setProperty("--uai-spot-x", "50%");
    el.style.setProperty("--uai-spot-y", "50%");

    let frame: number | null = null;
    let last: { x: number; y: number } | null = null;
    let rect: DOMRect | null = null;

    const update = () => {
      frame = null;
      if (!el || !last) return;
      el.style.setProperty("--uai-spot-x", `${last.x}px`);
      el.style.setProperty("--uai-spot-y", `${last.y}px`);
    };

    const onEnter = () => {
      rect = el.getBoundingClientRect();
    };

    const onMove = (e: PointerEvent) => {
      if (!rect) rect = el.getBoundingClientRect();
      last = { x: e.clientX - rect.left, y: e.clientY - rect.top };
      if (frame != null) return;
      frame = window.requestAnimationFrame(update);
    };

    const onLeave = () => {
      rect = null;
      last = null;
      el.style.setProperty("--uai-spot-x", "50%");
      el.style.setProperty("--uai-spot-y", "50%");
      if (frame != null) {
        window.cancelAnimationFrame(frame);
        frame = null;
      }
    };

    el.addEventListener("pointerenter", onEnter, { passive: true });
    el.addEventListener("pointermove", onMove, { passive: true });
    el.addEventListener("pointerleave", onLeave, { passive: true });

    return () => {
      el.removeEventListener("pointerenter", onEnter);
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerleave", onLeave);
      if (frame != null) window.cancelAnimationFrame(frame);
    };
  }, [targetId]);

  return null;
}

