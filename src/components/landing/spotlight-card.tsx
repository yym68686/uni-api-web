"use client";

import * as React from "react";

import { cn } from "@/lib/utils";

interface SpotlightCardProps {
  children: React.ReactNode;
  className?: string;
}

function useReducedMotion() {
  const [reduced, setReduced] = React.useState(false);
  React.useEffect(() => {
    const media = window.matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReduced(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  return reduced;
}

function supportsSpotlight() {
  if (typeof window === "undefined") return false;
  return window.matchMedia("(pointer: fine)").matches;
}

export function SpotlightCard({ children, className }: SpotlightCardProps) {
  const reducedMotion = useReducedMotion();
  const ref = React.useRef<HTMLDivElement | null>(null);
  const frame = React.useRef<number | null>(null);
  const last = React.useRef<{ x: number; y: number } | null>(null);

  const enabled = !reducedMotion && supportsSpotlight();

  React.useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.setProperty("--uai-spot-x", "50%");
    el.style.setProperty("--uai-spot-y", "50%");
  }, []);

  function update(x: number, y: number) {
    const el = ref.current;
    if (!el) return;
    el.style.setProperty("--uai-spot-x", `${x}px`);
    el.style.setProperty("--uai-spot-y", `${y}px`);
  }

  function onMove(e: React.MouseEvent<HTMLDivElement>) {
    if (!enabled) return;
    const el = ref.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    last.current = { x: e.clientX - rect.left, y: e.clientY - rect.top };
    if (frame.current != null) return;
    frame.current = window.requestAnimationFrame(() => {
      frame.current = null;
      const next = last.current;
      if (!next) return;
      update(next.x, next.y);
    });
  }

  return (
    <div
      ref={ref}
      onMouseMove={onMove}
      className={cn(
        "group relative overflow-hidden rounded-2xl border border-border",
        "bg-card/40 backdrop-blur-xl",
        "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)]",
        "hover:-translate-y-1 hover:shadow-lg hover:border-primary/20",
        className
      )}
    >
      <div
        aria-hidden="true"
        className={cn(
          "pointer-events-none absolute inset-0 opacity-0 transition-opacity duration-300",
          "group-hover:opacity-100"
        )}
        style={{
          backgroundImage:
            "radial-gradient(320px circle at var(--uai-spot-x) var(--uai-spot-y), oklch(var(--primary) / 0.18), transparent 62%)"
        }}
      />
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-foreground/10 to-transparent"
      />
      <div className="relative">{children}</div>
    </div>
  );
}

