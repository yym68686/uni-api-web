"use client";

import * as React from "react";
import { Moon, Sun } from "lucide-react";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useI18n } from "@/components/i18n/i18n-provider";

type Theme = "dark" | "light";

const STORAGE_KEY = "uai-theme";

type ViewTransitionMask = "blur" | "circle";

function getViewTransitionMask(): ViewTransitionMask {
  const ua = navigator.userAgent;
  const isWebKit =
    ua.includes("AppleWebKit") &&
    !ua.includes("Chrome") &&
    !ua.includes("Chromium") &&
    !ua.includes("Edg") &&
    !ua.includes("OPR");
  if (isWebKit) return "circle";
  if (ua.includes("Firefox")) return "circle";
  return "blur";
}

interface ThemeToggleProps {
  className?: string;
}

export function ThemeToggle({ className }: ThemeToggleProps) {
  const [theme, setTheme] = React.useState<Theme>(() => {
    if (typeof document === "undefined") return "dark";
    return document.documentElement.classList.contains("dark") ? "dark" : "light";
  });
  const buttonRef = React.useRef<HTMLButtonElement | null>(null);
  const { t } = useI18n();

  function applyViewTransitionMaskVariant() {
    const mask = getViewTransitionMask();
    if (mask === "blur") {
      delete document.documentElement.dataset.uaiVtMask;
      return;
    }
    document.documentElement.dataset.uaiVtMask = mask;
  }

  React.useEffect(() => {
    applyViewTransitionMaskVariant();
  }, []);

  function switchTheme() {
    setTheme((current) => {
      const next: Theme = current === "dark" ? "light" : "dark";
      localStorage.setItem(STORAGE_KEY, next);
      document.documentElement.classList.toggle("dark", next === "dark");
      return next;
    });
  }

  const Icon = theme === "dark" ? Sun : Moon;
  const label = theme === "dark" ? t("theme.toLight") : t("theme.toDark");

  function setTransitionOrigin(event: React.MouseEvent<HTMLButtonElement>) {
    const root = document.documentElement;
    let x = Math.round(event.clientX);
    let y = Math.round(event.clientY);

    const isInvalid = !Number.isFinite(x) || !Number.isFinite(y) || (x === 0 && y === 0);
    if (isInvalid) {
      const rect = buttonRef.current?.getBoundingClientRect() ?? null;
      if (rect) {
        x = Math.round(rect.left + rect.width / 2);
        y = Math.round(rect.top + rect.height / 2);
      } else {
        x = Math.round(window.innerWidth / 2);
        y = Math.round(window.innerHeight / 2);
      }
    }

    root.style.setProperty("--uai-vt-x", `${x}px`);
    root.style.setProperty("--uai-vt-y", `${y}px`);
  }

  function toggleWithViewTransition(event: React.MouseEvent<HTMLButtonElement>) {
    const prefersReducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (prefersReducedMotion || typeof document.startViewTransition !== "function") {
      switchTheme();
      return;
    }

    applyViewTransitionMaskVariant();
    setTransitionOrigin(event);
    document.startViewTransition(() => {
      switchTheme();
    });
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className={cn("rounded-xl", className)}
          onClick={toggleWithViewTransition}
          aria-label={label}
          ref={buttonRef}
        >
          <Icon className="h-5 w-5" />
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}
