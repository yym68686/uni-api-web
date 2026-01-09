"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import * as React from "react";
import { Boxes, KeyRound, LayoutDashboard, Megaphone, PlugZap, ScrollText, SlidersHorizontal, Users } from "lucide-react";

import { cn } from "@/lib/utils";

const navItems = [
  { href: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
  { href: "/keys", label: "API Keys", icon: KeyRound },
  { href: "/models", label: "Models", icon: Boxes },
  { href: "/logs", label: "Logs", icon: ScrollText },
] as const;

const adminItems = [
  { href: "/admin/channels", label: "Channels", icon: PlugZap },
  { href: "/admin/models", label: "Model Config", icon: SlidersHorizontal },
  { href: "/admin/users", label: "Users", icon: Users },
  { href: "/admin/announcements", label: "Announcements", icon: Megaphone }
] as const;

interface AppSidebarContentProps {
  onNavigate?: () => void;
}

export function AppSidebarContent({ onNavigate }: AppSidebarContentProps) {
  const pathname = usePathname();
  const [role, setRole] = React.useState<string | null>(null);
  const isAdmin = role === "admin" || role === "owner";

  React.useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const res = await fetch("/api/auth/me", { cache: "no-store" });
        if (!res.ok) return;
        const json: unknown = await res.json();
        if (!json || typeof json !== "object") return;
        const r = (json as { role?: unknown }).role;
        if (!cancelled && typeof r === "string") setRole(r);
      } catch {
        // ignore
      }
    }
    void load();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border p-4">
        <Link
          href="/"
          className="group flex items-center gap-3"
          onClick={onNavigate}
        >
          <div
            className={cn(
              "flex h-10 w-10 items-center justify-center rounded-xl",
              "bg-gradient-to-br from-primary to-primary/80 text-primary-foreground shadow-sm",
              "transition-transform duration-300 group-hover:scale-[1.04]"
            )}
          >
            <span className="font-logo text-[10px] leading-none tracking-tight">
              UA
            </span>
          </div>
          <div className="min-w-0">
            <div className="truncate font-semibold text-sidebar-foreground">
              Uni API Console
            </div>
            <div className="truncate text-xs text-muted-foreground">
              Deep Indigo Dark
            </div>
          </div>
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
                onClick={onNavigate}
                className={cn(
                  "flex items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium",
                  "transition-colors",
                  active
                    ? "bg-sidebar-accent text-sidebar-accent-foreground"
                    : "text-sidebar-foreground/80 hover:bg-sidebar-accent/70 hover:text-sidebar-accent-foreground"
                )}
              >
                <Icon className="h-4 w-4" />
                <span className="truncate">{item.label}</span>
              </Link>
            );
          })}
        </div>

        {isAdmin ? (
          <div className="mt-4 space-y-1">
            <div className="px-3 py-2 text-xs font-semibold text-muted-foreground">
              Admin
            </div>
            {adminItems.map((item) => {
              const active = pathname.startsWith(item.href);
              const Icon = item.icon;
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  onClick={onNavigate}
                  className={cn(
                    "flex items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium",
                    "transition-colors",
                    active
                      ? "bg-sidebar-accent text-sidebar-accent-foreground"
                      : "text-sidebar-foreground/80 hover:bg-sidebar-accent/70 hover:text-sidebar-accent-foreground"
                  )}
                >
                  <Icon className="h-4 w-4" />
                  <span className="truncate">{item.label}</span>
                </Link>
              );
            })}
          </div>
        ) : null}
      </nav>

      <div className="border-t border-border p-4">
        <div className="rounded-xl border border-border bg-background/40 p-3">
          <div className="text-xs text-muted-foreground">Workspace</div>
          <div className="mt-1 truncate text-sm font-medium text-sidebar-foreground">
            Default
          </div>
        </div>
      </div>
    </div>
  );
}

export function AppSidebar() {
  return (
    <aside className="fixed inset-y-0 left-0 hidden w-64 border-r border-border bg-sidebar sm:block">
      <AppSidebarContent />
    </aside>
  );
}
