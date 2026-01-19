import "@/app/globals.css";

import type { Metadata } from "next";
import { Press_Start_2P } from "next/font/google";
import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import { Toaster } from "sonner";

import { cn } from "@/lib/utils";
import { getAppName, getPublicApiBaseUrl } from "@/lib/app-config";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { I18nProvider } from "@/components/i18n/i18n-provider";

const pressStart2p = Press_Start_2P({
  weight: "400",
  subsets: ["latin"],
  display: "swap",
  variable: "--font-press-start"
});

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
  const publicApiBaseUrl = getPublicApiBaseUrl();
  return (
    <html
      lang={locale}
      data-public-api-base-url={publicApiBaseUrl ?? ""}
      className={cn("dark", GeistSans.variable, GeistMono.variable, pressStart2p.variable)}
      suppressHydrationWarning
    >
      <body
        className={cn("min-h-dvh bg-background font-sans text-foreground")}
        suppressHydrationWarning
      >
        <I18nProvider locale={locale}>
          <a
            href="#main"
            className={cn(
              "sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-[100]",
              "rounded-xl border border-border bg-background/80 px-4 py-2 text-sm text-foreground backdrop-blur",
              "shadow-[0_0_0_1px_oklch(var(--border)/0.55),0_12px_34px_oklch(var(--background)/0.65)]"
            )}
          >
            {t(locale, "common.skipToContent")}
          </a>
          <Toaster theme="dark" richColors closeButton />
          {children}
        </I18nProvider>
      </body>
    </html>
  );
}
