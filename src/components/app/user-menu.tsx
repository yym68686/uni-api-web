"use client";

import { LogOut, User } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/i18n/i18n-provider";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";

interface UserMenuProps {
  userName: string;
  className?: string;
}

function initials(name: string) {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  const first = parts[0]?.[0] ?? "U";
  const last = parts[parts.length - 1]?.[0] ?? "";
  return (first + last).toUpperCase();
}

export function UserMenu({ userName, className }: UserMenuProps) {
  const router = useRouter();
  const { t } = useI18n();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className={cn("h-10 w-10 rounded-full", className)}
          aria-label="User menu"
        >
          <span className="flex h-9 w-9 items-center justify-center rounded-full border border-border bg-muted/30 font-mono text-xs text-foreground">
            {initials(userName)}
          </span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel>
          <div className="text-xs text-muted-foreground">{t("auth.signedInAs")}</div>
          <div className="truncate text-sm font-medium text-foreground">{userName}</div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link href="/profile">
            <User className="mr-2 h-4 w-4" />
            {t("app.profile")}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          className="text-destructive focus:text-destructive"
          onClick={async () => {
            try {
              const res = await fetch("/api/auth/logout", { method: "POST" });
              if (!res.ok) throw new Error(await res.text());
              toast.success(t("auth.logoutSuccess"));
              router.replace("/login");
            } catch (err) {
              toast.error(err instanceof Error ? err.message : t("auth.logoutFailed"));
            }
          }}
        >
          <LogOut className="mr-2 h-4 w-4" />
          {t("auth.logout")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
