"use client";

import * as React from "react";
import { Copy } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/i18n/i18n-provider";
import { cn } from "@/lib/utils";

interface CodeBlockProps {
  code: string;
  lang?: string;
  className?: string;
}

export function CodeBlock({ code, lang, className }: CodeBlockProps) {
  const { t } = useI18n();
  const [copied, setCopied] = React.useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      toast.success(t("common.copied"));
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      toast.error(t("common.copyFailed"));
    }
  }

  return (
    <div
      className={cn(
        "group relative overflow-hidden rounded-xl border border-border bg-card/40 backdrop-blur",
        "shadow-[0_0_0_1px_oklch(var(--border)/0.55),0_10px_30px_oklch(0%_0_0/0.28)]",
        className
      )}
    >
      <div className="flex items-center justify-between gap-2 border-b border-border bg-background/30 px-3 py-2">
        <span className="truncate text-xs text-muted-foreground">
          <span className="font-mono">{lang ?? "code"}</span>
        </span>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-8 rounded-lg px-2"
          onClick={copy}
          aria-label={t("common.copy")}
        >
          <Copy suppressHydrationWarning className="h-4 w-4" />
          <span className="text-xs">{copied ? t("common.copied") : t("common.copy")}</span>
        </Button>
      </div>
      <pre className="scrollbar-hide overflow-x-auto p-4 text-xs leading-relaxed">
        <code className="font-mono text-foreground">{code}</code>
      </pre>
    </div>
  );
}
