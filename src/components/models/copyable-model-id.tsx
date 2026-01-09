"use client";

import { Copy } from "lucide-react";
import { toast } from "sonner";

import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";

interface CopyableModelIdProps {
  value: string;
  className?: string;
}

export function CopyableModelId({ value, className }: CopyableModelIdProps) {
  const { t } = useI18n();

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      toast.success(t("common.copied"));
    } catch {
      toast.error(t("common.copyFailed"));
    }
  }

  return (
    <button
      type="button"
      onClick={() => void copy()}
      className={cn(
        "group inline-flex max-w-full items-center gap-2 rounded-lg px-1 py-0.5 text-left",
        "font-mono text-xs text-foreground",
        "transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40",
        className
      )}
      aria-label={`Copy model: ${value}`}
    >
      <span className="truncate">{value}</span>
      <Copy className="h-3.5 w-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
    </button>
  );
}
