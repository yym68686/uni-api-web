"use client";

import * as React from "react";
import { Check, Languages } from "lucide-react";
import { useRouter } from "next/navigation";

import { LOCALE_COOKIE_NAME, normalizeLocale, type Locale } from "@/lib/i18n/messages";
import { useI18n } from "@/components/i18n/i18n-provider";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

type LocaleMode = "auto" | Locale;

function readCookieValue(name: string) {
  if (typeof document === "undefined") return null;
  const prefix = `${encodeURIComponent(name)}=`;
  const parts = document.cookie.split(";").map((p) => p.trim());
  for (const part of parts) {
    if (!part.startsWith(prefix)) continue;
    return decodeURIComponent(part.slice(prefix.length));
  }
  return null;
}

function setCookie(name: string, value: string) {
  const encoded = `${encodeURIComponent(name)}=${encodeURIComponent(value)}`;
  document.cookie = `${encoded}; Path=/; Max-Age=31536000; SameSite=Lax`;
}

function deleteCookie(name: string) {
  document.cookie = `${encodeURIComponent(name)}=; Path=/; Max-Age=0; SameSite=Lax`;
}

function getModeFromCookie(): LocaleMode {
  const raw = readCookieValue(LOCALE_COOKIE_NAME);
  const normalized = normalizeLocale(raw);
  return normalized ?? "auto";
}

interface LanguageToggleProps {
  className?: string;
}

export function LanguageToggle({ className }: LanguageToggleProps) {
  const router = useRouter();
  const { locale, t } = useI18n();
  const [mode, setMode] = React.useState<LocaleMode>("auto");

  React.useEffect(() => {
    setMode(getModeFromCookie());
  }, []);

  const label =
    mode === "auto"
      ? `${t("common.language")}: ${t("common.lang.auto")} (${locale})`
      : `${t("common.language")}: ${mode}`;

  function apply(next: LocaleMode) {
    setMode(next);
    if (next === "auto") deleteCookie(LOCALE_COOKIE_NAME);
    else setCookie(LOCALE_COOKIE_NAME, next);
    queueMicrotask(() => router.refresh());
  }

  function checked(next: LocaleMode) {
    if (mode === "auto" && next === "auto") return true;
    if (mode !== "auto" && next !== "auto") return mode === next;
    return false;
  }

  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className={cn("rounded-xl", className)}
              aria-label={label}
            >
              <Languages className="h-5 w-5" />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>{label}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuItem
          onSelect={(e) => {
            e.preventDefault();
            apply("auto");
          }}
        >
          {checked("auto") ? <Check className="mr-2 h-4 w-4" /> : <span className="mr-2 h-4 w-4" />}
          {t("common.lang.auto")}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={(e) => {
            e.preventDefault();
            apply("zh-CN");
          }}
        >
          {checked("zh-CN") ? <Check className="mr-2 h-4 w-4" /> : <span className="mr-2 h-4 w-4" />}
          {t("common.lang.zh")}
        </DropdownMenuItem>
        <DropdownMenuItem
          onSelect={(e) => {
            e.preventDefault();
            apply("en");
          }}
        >
          {checked("en") ? <Check className="mr-2 h-4 w-4" /> : <span className="mr-2 h-4 w-4" />}
          {t("common.lang.en")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
