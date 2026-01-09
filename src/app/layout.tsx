import "@/app/globals.css";
import "@fontsource/press-start-2p/400.css";

import type { Metadata } from "next";
import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import { Toaster } from "sonner";

import { cn } from "@/lib/utils";
import { getAppName } from "@/lib/app-config";
import { getRequestLocale } from "@/lib/i18n/server";
import { I18nProvider } from "@/components/i18n/i18n-provider";

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

export default async function RootLayout({ children }: RootLayoutProps) {
  const locale = await getRequestLocale();
  return (
    <html
      lang={locale}
      className={cn("dark", GeistSans.variable, GeistMono.variable)}
      suppressHydrationWarning
    >
      <body
        className={cn("min-h-dvh bg-background font-sans text-foreground")}
        suppressHydrationWarning
      >
        <I18nProvider locale={locale}>
          {children}
          <Toaster theme="dark" richColors closeButton />
        </I18nProvider>
      </body>
    </html>
  );
}
