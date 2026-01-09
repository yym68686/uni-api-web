import { cn } from "@/lib/utils";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";

interface BrandPanelProps {
  appName: string;
  className?: string;
}

export async function BrandPanel({ appName, className }: BrandPanelProps) {
  const locale = await getRequestLocale();
  return (
    <section
      className={cn(
        "relative hidden min-h-dvh w-full overflow-hidden lg:block",
        "uai-brand-panel",
        className
      )}
      aria-hidden="true"
    >
      <div className="pointer-events-none absolute inset-0 uai-brand-grid" />
      <div className="pointer-events-none absolute left-1/2 top-1/2 h-[420px] w-[420px] -translate-x-1/2 -translate-y-1/2 rounded-full uai-orb" />

      <div className="absolute bottom-10 left-10 max-w-md">
        <div className="text-xs font-mono text-muted-foreground">
          {appName} · Console Access
        </div>
        <div className="mt-2 text-2xl font-semibold tracking-tight text-foreground">
          {t(locale, "auth.panel.title")}
        </div>
        <div className="mt-2 text-sm text-muted-foreground">
          {t(locale, "auth.panel.desc")}
        </div>
      </div>
    </section>
  );
}
