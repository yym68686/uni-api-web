"use client";

import * as React from "react";
import { CreditCard } from "lucide-react";
import Link from "next/link";

import { TooltipProvider } from "@/components/ui/tooltip";
import { Breadcrumbs } from "@/components/app/breadcrumbs";
import { MobileSidebar } from "@/components/app/mobile-sidebar";
import { ThemeToggle } from "@/components/app/theme-toggle";
import { LanguageToggle } from "@/components/app/language-toggle";
import { UserMenu } from "@/components/app/user-menu";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { LocaleMode } from "@/lib/i18n/messages";
import { ensureDeviceIdCookie } from "@/lib/device-id";

interface AppTopbarProps {
  userName: string;
  appName: string;
  userRole: string | null;
  initialLocaleMode: LocaleMode;
}

export function AppTopbar({ userName, userRole, appName, initialLocaleMode }: AppTopbarProps) {
  const { t } = useI18n();

  React.useEffect(() => {
    ensureDeviceIdCookie();
  }, []);

  return (
    <TooltipProvider delayDuration={150}>
      <header className="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-3 px-4 sm:px-6">
          <MobileSidebar appName={appName} userRole={userRole} />
          <Breadcrumbs className="min-w-0 flex-1" />
          <LanguageToggle initialMode={initialLocaleMode} />
          <ThemeToggle />
          <Button
            asChild
            variant="outline"
            className={
              "h-10 shrink-0 rounded-xl border-primary/20 bg-primary/10 px-3 text-primary " +
              "shadow-[0_0_0_1px_oklch(var(--primary)/0.18),0_12px_32px_oklch(var(--primary)/0.18),inset_0_1px_0_0_oklch(var(--foreground)/0.08)] " +
              "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)] " +
              "hover:-translate-y-1 hover:border-primary/35 hover:bg-primary hover:text-primary-foreground " +
              "focus-visible:ring-primary/40 motion-reduce:transform-none motion-reduce:transition-none sm:h-9"
            }
          >
            <Link href="/billing#billing-topup" aria-label={t("billing.topup.title")}>
              <CreditCard className="h-4 w-4" />
              <span className="hidden sm:inline">{t("billing.topup.title")}</span>
            </Link>
          </Button>
          <UserMenu userName={userName} />
        </div>
      </header>
    </TooltipProvider>
  );
}
