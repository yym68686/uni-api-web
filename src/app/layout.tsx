import "@/app/globals.css";
import "@fontsource/press-start-2p/400.css";

import type { Metadata } from "next";
import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import { Toaster } from "sonner";

import { cn } from "@/lib/utils";
import { getAppName } from "@/lib/app-config";

export function generateMetadata(): Metadata {
  const appName = getAppName();
  return {
    title: {
      default: appName,
      template: `%s · ${appName}`
    },
    icons: {
      icon: "/favicon.ico"
    },
    description: "LLM API usage dashboard and key management console."
  };
}

interface RootLayoutProps {
  children: React.ReactNode;
}

export default function RootLayout({ children }: RootLayoutProps) {
  return (
    <html
      lang="zh-CN"
      className={cn("dark", GeistSans.variable, GeistMono.variable)}
      suppressHydrationWarning
    >
      <body
        className={cn("min-h-dvh bg-background font-sans text-foreground")}
        suppressHydrationWarning
      >
        {children}
        <Toaster theme="dark" richColors closeButton />
      </body>
    </html>
  );
}
