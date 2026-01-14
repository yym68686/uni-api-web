"use client";

import * as React from "react";
import { Menu } from "lucide-react";

import { AppSidebarContent } from "@/components/app/app-sidebar";
import { useI18n } from "@/components/i18n/i18n-provider";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";

interface MobileSidebarProps {
  appName: string;
  userRole: string | null;
}

export function MobileSidebar({ appName, userRole }: MobileSidebarProps) {
  const { t } = useI18n();
  const [open, setOpen] = React.useState(false);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="rounded-xl sm:hidden"
          aria-label={t("common.openMenu")}
        >
          <Menu className="h-5 w-5" />
        </Button>
      </DialogTrigger>
      <DialogContent
        className="left-0 top-0 h-dvh w-72 max-w-[85vw] translate-x-0 translate-y-0 rounded-none border-r border-border bg-sidebar p-0"
      >
        <DialogHeader className="sr-only">
          <DialogTitle>{t("common.menu")}</DialogTitle>
        </DialogHeader>
        <AppSidebarContent appName={appName} userRole={userRole} onNavigate={() => setOpen(false)} />
      </DialogContent>
    </Dialog>
  );
}
