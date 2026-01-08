"use client";

import { TooltipProvider } from "@/components/ui/tooltip";
import { Breadcrumbs } from "@/components/app/breadcrumbs";
import { MobileSidebar } from "@/components/app/mobile-sidebar";
import { ThemeToggle } from "@/components/app/theme-toggle";
import { UserMenu } from "@/components/app/user-menu";

interface AppTopbarProps {
  userName: string;
}

export function AppTopbar({ userName }: AppTopbarProps) {
  return (
    <TooltipProvider delayDuration={150}>
      <header className="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-3 px-4 sm:px-6">
          <MobileSidebar />
          <Breadcrumbs className="min-w-0 flex-1" />
          <ThemeToggle />
          <UserMenu userName={userName} />
        </div>
      </header>
    </TooltipProvider>
  );
}
