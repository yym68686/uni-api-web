"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { Boxes, CreditCard, KeyRound, LayoutDashboard, Megaphone, PlugZap, ScrollText, Settings, SlidersHorizontal, Users, type LucideIcon } from "lucide-react";

import { cn } from "@/lib/utils";
import { BrandWordmark } from "@/components/brand/wordmark";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { MessageKey } from "@/lib/i18n/messages";

const navItems = [
  { href: "/dashboard", labelKey: "app.dashboard", icon: LayoutDashboard },
  { href: "/keys", labelKey: "app.keys", icon: KeyRound },
  { href: "/models", labelKey: "app.models", icon: Boxes },
  { href: "/logs", labelKey: "app.logs", icon: ScrollText },
  { href: "/billing", labelKey: "app.billing", icon: CreditCard },
] as const satisfies ReadonlyArray<{ href: string; labelKey: MessageKey; icon: LucideIcon }>;

const adminItems = [
  { href: "/admin/channels", labelKey: "app.admin.channels", icon: PlugZap },
  { href: "/admin/models", labelKey: "app.admin.modelConfig", icon: SlidersHorizontal },
  { href: "/admin/users", labelKey: "app.admin.users", icon: Users },
  { href: "/admin/announcements", labelKey: "app.admin.announcements", icon: Megaphone },
  { href: "/admin/settings", labelKey: "app.admin.settings", icon: Settings }
] as const satisfies ReadonlyArray<{ href: string; labelKey: MessageKey; icon: LucideIcon }>;

interface AppSidebarContentProps {
  appName: string;
  userRole: string | null;
  onNavigate?: () => void;
}

export function AppSidebarContent({ appName, userRole, onNavigate }: AppSidebarContentProps) {
  const pathname = usePathname();
  const router = useRouter();
  const isAdmin = userRole === "admin" || userRole === "owner";
  const { t } = useI18n();

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border p-4">
        <Link
          href="/"
          prefetch={false}
          className="group flex w-full items-center justify-center"
          onClick={onNavigate}
          onMouseEnter={() => router.prefetch("/")}
          onFocus={() => router.prefetch("/")}
          onTouchStart={() => router.prefetch("/")}
        >
          <BrandWordmark
            name={appName}
            className="text-lg transition-colors duration-200 group-hover:text-foreground"
          />
        </Link>
      </div>

      <nav className="scrollbar-hide flex-1 overflow-auto p-2">
        <div className="space-y-1">
          {navItems.map((item) => {
            const active = pathname === item.href || pathname.startsWith(`${item.href}/`);
            const Icon = item.icon;
            return (
              <Link
                key={item.href}
                href={item.href}
                prefetch={false}
                onClick={onNavigate}
                onMouseEnter={() => router.prefetch(item.href)}
                onFocus={() => router.prefetch(item.href)}
                onTouchStart={() => router.prefetch(item.href)}
                className={cn(
                  "flex items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium",
                  "transition-colors",
                  active
                    ? "bg-sidebar-accent text-sidebar-accent-foreground"
                    : "text-sidebar-foreground/80 hover:bg-sidebar-accent/70 hover:text-sidebar-accent-foreground"
                )}
              >
                <Icon className="h-4 w-4" />
                <span className="truncate">{t(item.labelKey)}</span>
              </Link>
            );
          })}
        </div>

        {isAdmin ? (
          <div className="mt-4 space-y-1">
            <div className="px-3 py-2 text-xs font-semibold text-muted-foreground">{t("app.admin")}</div>
            {adminItems.map((item) => {
              const active = pathname.startsWith(item.href);
              const Icon = item.icon;
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  prefetch={false}
                  onClick={onNavigate}
                  onMouseEnter={() => router.prefetch(item.href)}
                  onFocus={() => router.prefetch(item.href)}
                  onTouchStart={() => router.prefetch(item.href)}
                  className={cn(
                    "flex items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium",
                    "transition-colors",
                    active
                      ? "bg-sidebar-accent text-sidebar-accent-foreground"
                      : "text-sidebar-foreground/80 hover:bg-sidebar-accent/70 hover:text-sidebar-accent-foreground"
                  )}
                >
                  <Icon className="h-4 w-4" />
                  <span className="truncate">{t(item.labelKey)}</span>
                </Link>
              );
            })}
          </div>
        ) : null}
      </nav>

      <div className="border-t border-border p-4">
        <div className="rounded-xl border border-border bg-background/40 p-3">
          <div className="text-xs text-muted-foreground">{t("app.workspace")}</div>
          <div className="mt-1 truncate text-sm font-medium text-sidebar-foreground">
            {t("app.workspace.default")}
          </div>
        </div>
      </div>
    </div>
  );
}

interface AppSidebarProps {
  appName: string;
  userRole: string | null;
}

export function AppSidebar({ appName, userRole }: AppSidebarProps) {
  return (
    <aside className="fixed inset-y-0 left-0 hidden w-64 border-r border-border bg-sidebar sm:block">
      <AppSidebarContent appName={appName} userRole={userRole} />
    </aside>
  );
}
