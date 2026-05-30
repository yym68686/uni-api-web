import "@/app/globals.css";
import "@fontsource/press-start-2p/latin-400.css";

import type { Metadata } from "next";
import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import { Toaster } from "sonner";

import { cn } from "@/lib/utils";
import {
  getDisplayCnyPerUsd,
  getAppName,
  getPublicApiBaseUrl,
  getPublicAppBaseUrl
} from "@/lib/app-config";
import { getRequestLocale } from "@/lib/i18n/server";
import { I18nProvider } from "@/components/i18n/i18n-provider";
import { SkipToContentLink } from "@/components/i18n/skip-to-content-link";
import { DataOceanTracker } from "@/components/analytics/dataocean-tracker";
import { CurrencyProvider } from "@/components/currency/currency-provider";

const LOCALE_INIT_SCRIPT = `
(function () {
  try {
    var path = window.location.pathname || "/";
    var locale = "";
    if (path === "/zh-CN" || path.indexOf("/zh-CN/") === 0 || path.indexOf("/docs/zh-CN") === 0) {
      locale = "zh-CN";
    } else if (path === "/en" || path.indexOf("/en/") === 0 || path.indexOf("/docs/en") === 0) {
      locale = "en";
    }
    if (locale) {
      document.documentElement.lang = locale;
      try {
        window.sessionStorage.setItem("uai_locale_hint", locale);
      } catch (e) {}
    }
  } catch (e) {}
})();
`.trim();

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

const DOCUMENT_INIT_SCRIPT = `${LOCALE_INIT_SCRIPT}\n${THEME_INIT_SCRIPT}`;

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
  const displayCnyPerUsd = getDisplayCnyPerUsd();
  return (
    <html
      lang={locale}
      data-public-api-base-url={publicApiBaseUrl ?? ""}
      data-display-cny-per-usd={String(displayCnyPerUsd)}
      className={cn("dark", GeistSans.variable, GeistMono.variable)}
      suppressHydrationWarning
    >
      <head>
        <script
          dangerouslySetInnerHTML={{ __html: DOCUMENT_INIT_SCRIPT }}
        />
      </head>
      <body
        className={cn("min-h-dvh bg-background font-sans text-foreground")}
        suppressHydrationWarning
      >
        <I18nProvider locale={locale}>
          <CurrencyProvider initialCnyPerUsd={displayCnyPerUsd}>
            <SkipToContentLink />
            <Toaster theme="dark" richColors closeButton />
            <DataOceanTracker />
            {children}
          </CurrencyProvider>
        </I18nProvider>
      </body>
    </html>
  );
}
