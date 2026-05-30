"use client";

import { useI18n } from "@/components/i18n/i18n-provider";
import { cn } from "@/lib/utils";

export function SkipToContentLink() {
  const { t } = useI18n();

  return (
    <a
      href="#main"
      className={cn(
        "sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-[100]",
        "rounded-xl border border-border bg-background/80 px-4 py-2 text-sm text-foreground backdrop-blur",
        "shadow-[0_0_0_1px_oklch(var(--border)/0.55),0_12px_34px_oklch(var(--background)/0.65)]"
      )}
    >
      {t("common.skipToContent")}
    </a>
  );
}
