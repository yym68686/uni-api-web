import "@/app/globals.css";
import "@fontsource/press-start-2p/latin-400.css";

import type { Metadata } from "next";
import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import { Toaster } from "sonner";

import { cn } from "@/lib/utils";
import { getAppName, getPublicApiBaseUrl, getPublicAppBaseUrl } from "@/lib/app-config";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { I18nProvider } from "@/components/i18n/i18n-provider";

const THEME_INIT_SCRIPT = `
(function () {
  try {
    var storageKey = "uai-theme";
    var saved = localStorage.getItem(storageKey);
    var theme = saved === "light" || saved === "dark" ? saved : "dark";
    var root = document.documentElement;
    root.classList.toggle("dark", theme === "dark");

    var ua = navigator.userAgent || "";
    var isWebKit =
      ua.indexOf("AppleWebKit") !== -1 &&
      ua.indexOf("Chrome") === -1 &&
      ua.indexOf("Chromium") === -1 &&
      ua.indexOf("Edg") === -1 &&
      ua.indexOf("OPR") === -1;
    var mask = isWebKit || ua.indexOf("Firefox") !== -1 ? "circle" : "blur";
    if (mask === "circle") {
      root.setAttribute("data-uai-vt-mask", "circle");
    } else {
      root.removeAttribute("data-uai-vt-mask");
    }
  } catch (e) {}
})();
`.trim();

export function generateMetadata(): Metadata {
  const appName = getAppName();
  const base = getPublicAppBaseUrl();
  return {
    metadataBase: base ? new URL(base) : undefined,
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
      className={cn("dark", GeistSans.variable, GeistMono.variable)}
      suppressHydrationWarning
    >
      <head>
        <script
          dangerouslySetInnerHTML={{ __html: THEME_INIT_SCRIPT }}
        />
      </head>
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
