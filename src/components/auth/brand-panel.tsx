import { cn } from "@/lib/utils";

interface BrandPanelProps {
  className?: string;
}

export function BrandPanel({ className }: BrandPanelProps) {
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
          Deep Indigo Dark · Console Access
        </div>
        <div className="mt-2 text-2xl font-semibold tracking-tight text-foreground">
          Secure, fast, and observable LLM API platform.
        </div>
        <div className="mt-2 text-sm text-muted-foreground">
          登录后可查看用量、管理 API Keys、配置计费与告警。
        </div>
      </div>
    </section>
  );
}
