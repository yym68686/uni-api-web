"use client";

import * as React from "react";
import { Menu } from "lucide-react";

import { AppSidebarContent } from "@/components/app/app-sidebar";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTrigger } from "@/components/ui/dialog";

interface MobileSidebarProps {
  appName: string;
}

export function MobileSidebar({ appName }: MobileSidebarProps) {
  const [open, setOpen] = React.useState(false);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="rounded-xl sm:hidden" aria-label="Open menu">
          <Menu className="h-5 w-5" />
        </Button>
      </DialogTrigger>
      <DialogContent
        className="left-0 top-0 h-dvh w-72 max-w-[85vw] translate-x-0 translate-y-0 rounded-none border-r border-border bg-sidebar p-0"
      >
        <AppSidebarContent appName={appName} onNavigate={() => setOpen(false)} />
      </DialogContent>
    </Dialog>
  );
}
