import Link from "next/link";
import {
  ArrowRight,
  BadgeCheck,
  BookOpen,
  KeyRound,
  Layers3,
  LineChart,
  ScrollText,
  Shield,
  Sparkles,
  Zap,
  Mail
} from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { IntentLink } from "@/components/landing/intent-link";
import { SpotlightSurface } from "@/components/landing/spotlight-surface";
import { cn } from "@/lib/utils";
import { getAppName } from "@/lib/app-config";
import { BrandWordmark } from "@/components/brand/wordmark";
import { t } from "@/lib/i18n/messages";
import { getRequestLocale } from "@/lib/i18n/server";

export default async function LandingPage() {
  const appName = getAppName();
  const locale = await getRequestLocale();

  return (
    <div className="relative min-h-dvh overflow-hidden bg-background">
      <div className="pointer-events-none absolute inset-0 uai-landing-canvas" />
      <div className="pointer-events-none absolute inset-0 uai-landing-grid-64 opacity-40" />
      <div className="pointer-events-none absolute inset-0 uai-landing-noise" />
      <div
        className={cn(
          "pointer-events-none absolute left-1/2 top-[-380px] h-[980px] w-[980px] -translate-x-1/2 rounded-full",
          "uai-landing-blob uai-landing-blob-a motion-reduce:animate-none"
        )}
      />
      <div
        className={cn(
          "pointer-events-none absolute left-[-320px] top-[120px] h-[760px] w-[760px] rounded-full",
          "uai-landing-blob uai-landing-blob-b motion-reduce:animate-none"
        )}
      />
      <div
        className={cn(
          "pointer-events-none absolute right-[-420px] top-[520px] h-[820px] w-[820px] rounded-full",
          "uai-landing-blob uai-landing-blob-c motion-reduce:animate-none"
        )}
      />

      <header className="sticky top-0 z-40 border-b border-border bg-background/70 backdrop-blur-xl">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-3 px-4 sm:px-6">
          <Link href="/" className="group flex items-center gap-3">
            <BrandWordmark
              name={appName}
              className="text-sm transition-colors duration-200 group-hover:text-foreground"
            />
          </Link>

          <nav className="hidden items-center gap-6 text-sm text-muted-foreground md:flex">
            <a href="#features" className="transition-colors hover:text-foreground">
              {t(locale, "landing.nav.features")}
            </a>
            <a href="#security" className="transition-colors hover:text-foreground">
              {t(locale, "landing.nav.security")}
            </a>
            <a href="#docs" className="transition-colors hover:text-foreground">
              {t(locale, "landing.nav.docs")}
            </a>
          </nav>

          <div className="flex items-center gap-2">
            <IntentLink
              href="/login"
              variant="outline"
              className={cn("rounded-xl bg-transparent", "border-border/80 hover:bg-background/40")}
            >
              {t(locale, "landing.signIn")}
            </IntentLink>
            <IntentLink
              href="/register"
              className={cn(
                "rounded-xl",
                "shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_12px_34px_oklch(var(--primary)/0.22),inset_0_1px_0_0_oklch(var(--foreground)/0.12)]",
                "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)] hover:-translate-y-0.5 hover:shadow-[0_0_0_1px_oklch(var(--primary)/0.45),0_18px_48px_oklch(var(--primary)/0.26),inset_0_1px_0_0_oklch(var(--foreground)/0.14)]"
              )}
            >
              {t(locale, "landing.getStarted")}
              <ArrowRight className="h-4 w-4" />
            </IntentLink>
          </div>
        </div>
      </header>

      <main id="main" className="relative mx-auto w-full max-w-6xl px-4 py-10 sm:px-6 sm:py-14">
        <section className="relative">
          <div className="pointer-events-none absolute -left-6 -top-10 hidden h-40 w-40 rounded-full bg-primary/10 blur-2xl lg:block" />
          <div className="pointer-events-none absolute -right-8 top-10 hidden h-52 w-52 rounded-full bg-primary/10 blur-3xl lg:block" />

          <div className="grid gap-8 lg:grid-cols-2 lg:items-center">
            <div className="space-y-7">
              <div className="inline-flex items-center gap-2 rounded-full border border-border bg-background/40 px-3 py-1 text-xs text-muted-foreground backdrop-blur">
                <Sparkles className="h-3.5 w-3.5 text-primary" />
                {t(locale, "landing.hero.badge")}
              </div>

              <h1 className="text-balance text-4xl font-semibold tracking-tight text-foreground sm:text-5xl lg:text-6xl">
                {t(locale, "landing.hero.title")}
              </h1>

              <p className="text-pretty text-base leading-relaxed text-muted-foreground sm:text-lg">
                {t(locale, "landing.hero.lead")}
              </p>

              <div className="grid gap-3 sm:grid-cols-3">
                <div className="rounded-xl border border-border bg-background/35 p-4 backdrop-blur">
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <BadgeCheck className="h-4 w-4 text-success" />
                    {t(locale, "landing.pill.keys.label")}
                  </div>
                  <div className="mt-2 text-sm font-medium text-foreground">{t(locale, "landing.pill.keys.title")}</div>
                  <div className="mt-1 text-xs text-muted-foreground">{t(locale, "landing.pill.keys.desc")}</div>
                </div>
                <div className="rounded-xl border border-border bg-background/35 p-4 backdrop-blur">
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <LineChart className="h-4 w-4 text-primary" />
                    {t(locale, "landing.pill.spend.label")}
                  </div>
                  <div className="mt-2 text-sm font-medium text-foreground">{t(locale, "landing.pill.spend.title")}</div>
                  <div className="mt-1 text-xs text-muted-foreground">{t(locale, "landing.pill.spend.desc")}</div>
                </div>
                <div className="rounded-xl border border-border bg-background/35 p-4 backdrop-blur">
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <ScrollText className="h-4 w-4 text-warning" />
                    {t(locale, "landing.pill.logs.label")}
                  </div>
                  <div className="mt-2 text-sm font-medium text-foreground">{t(locale, "landing.pill.logs.title")}</div>
                  <div className="mt-1 text-xs text-muted-foreground">{t(locale, "landing.pill.logs.desc")}</div>
                </div>
              </div>

              <div className="flex flex-col gap-3 sm:flex-row">
                <IntentLink
                  href="/register"
                  className={cn(
                    "rounded-xl uai-border-beam",
                    "shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_12px_34px_oklch(var(--primary)/0.22),inset_0_1px_0_0_oklch(var(--foreground)/0.12)]"
                  )}
                >
                  {t(locale, "register.title")}
                  <ArrowRight className="h-4 w-4" />
                </IntentLink>
                <IntentLink
                  href="/login"
                  variant="outline"
                  className="rounded-xl bg-transparent border-border/80 hover:bg-background/40"
                >
                  {t(locale, "landing.signIn")}
                </IntentLink>
              </div>
            </div>

            <SpotlightSurface className="rounded-2xl">
              <CardHeader className="pb-4">
                <CardTitle className="flex items-center gap-2">
                  <Layers3 className="h-5 w-5 text-muted-foreground" />
                  {t(locale, "landing.mock.title")}
                </CardTitle>
                <CardDescription>{t(locale, "landing.mock.desc")}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid gap-3 sm:grid-cols-3">
                  <div className="rounded-xl border border-border bg-background/35 p-4">
                    <div className="text-xs text-muted-foreground">{t(locale, "landing.mock.spend24h")}</div>
                    <div className="mt-2 text-lg font-semibold tracking-tight text-foreground">$0.23</div>
                    <div className="mt-1 text-xs text-muted-foreground">{t(locale, "landing.mock.stable")}</div>
                  </div>
                  <div className="rounded-xl border border-border bg-background/35 p-4">
                    <div className="text-xs text-muted-foreground">{t(locale, "landing.mock.requests")}</div>
                    <div className="mt-2 text-lg font-semibold tracking-tight text-foreground">128</div>
                    <div className="mt-1 text-xs text-muted-foreground">{t(locale, "landing.mock.last24h")}</div>
                  </div>
                  <div className="rounded-xl border border-border bg-background/35 p-4">
                    <div className="text-xs text-muted-foreground">{t(locale, "landing.mock.errorRate")}</div>
                    <div className="mt-2 text-lg font-semibold tracking-tight text-foreground">0%</div>
                    <div className="mt-1 text-xs text-muted-foreground">{t(locale, "landing.mock.healthy")}</div>
                  </div>
                </div>

                <div className="overflow-hidden rounded-xl border border-border bg-background/35">
                  <div className="flex items-center justify-between border-b border-border bg-background/40 px-3 py-2">
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <Zap className="h-4 w-4 text-primary" />
                      {t(locale, "landing.mock.requestLog")}
                    </div>
                    <div className="text-[11px] text-muted-foreground">{t(locale, "landing.mock.metrics")}</div>
                  </div>
                  <div className="space-y-2 p-3 text-xs text-muted-foreground">
                    <div className="flex items-center justify-between font-mono">
                      <span className="truncate">gemini-2.5-flash</span>
                      <span>ttft 312ms · tps 48.2 · $0.0012</span>
                    </div>
                    <div className="flex items-center justify-between font-mono">
                      <span className="truncate">claude-3-7-sonnet</span>
                      <span>ttft 640ms · tps 22.9 · $0.0098</span>
                    </div>
                    <div className="flex items-center justify-between font-mono">
                      <span className="truncate">deepseek-chat</span>
                      <span>ttft 280ms · tps 55.4 · $0.0007</span>
                    </div>
                  </div>
                </div>

                <div className="flex items-center justify-between text-xs text-muted-foreground">
                  <span>{t(locale, "landing.mock.gateway")}</span>
                  <span className="font-mono">/v1/*</span>
                </div>
              </CardContent>
            </SpotlightSurface>
          </div>
        </section>

        <section id="features" className="uai-cv-auto mt-12 sm:mt-16">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <div className="text-xs font-mono tracking-widest text-muted-foreground">{t(locale, "landing.features.tag")}</div>
              <h2 className="mt-2 text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
                {t(locale, "landing.features.title")}
              </h2>
              <p className="mt-2 max-w-2xl text-sm leading-relaxed text-muted-foreground sm:text-base">
                {t(locale, "landing.features.desc")}
              </p>
            </div>
            <div className="hidden sm:block">
              <div className="rounded-full border border-border bg-background/35 px-3 py-1 text-xs text-muted-foreground backdrop-blur">
                Built on OKLCH tokens
              </div>
            </div>
          </div>

          <div className="mt-6 grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            <Card
              className={cn(
                "group overflow-hidden rounded-2xl border-border bg-card/40 backdrop-blur-xl",
                "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)]",
                "hover:-translate-y-1 hover:shadow-lg hover:border-primary/20"
              )}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <LineChart className="h-5 w-5 text-primary" />
                  {t(locale, "landing.features.card.spend.title")}
                </CardTitle>
                <CardDescription>{t(locale, "landing.features.card.spend.desc")}</CardDescription>
              </CardHeader>
              <CardContent className="text-sm text-muted-foreground">
                {t(locale, "landing.features.card.spend.body")}
              </CardContent>
            </Card>

            <Card
              className={cn(
                "group overflow-hidden rounded-2xl border-border bg-card/40 backdrop-blur-xl",
                "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)]",
                "hover:-translate-y-1 hover:shadow-lg hover:border-primary/20"
              )}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <KeyRound className="h-5 w-5 text-warning" />
                  {t(locale, "landing.features.card.keys.title")}
                </CardTitle>
                <CardDescription>{t(locale, "landing.features.card.keys.desc")}</CardDescription>
              </CardHeader>
              <CardContent className="text-sm text-muted-foreground">
                {t(locale, "landing.features.card.keys.body")}
              </CardContent>
            </Card>

            <Card
              className={cn(
                "group overflow-hidden rounded-2xl border-border bg-card/40 backdrop-blur-xl",
                "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)]",
                "hover:-translate-y-1 hover:shadow-lg hover:border-primary/20",
                "md:col-span-2 lg:col-span-1"
              )}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <ScrollText className="h-5 w-5 text-muted-foreground" />
                  {t(locale, "landing.features.card.logs.title")}
                </CardTitle>
                <CardDescription>{t(locale, "landing.features.card.logs.desc")}</CardDescription>
              </CardHeader>
              <CardContent className="text-sm text-muted-foreground">
                {t(locale, "landing.features.card.logs.body")}
              </CardContent>
            </Card>
          </div>
        </section>

        <section id="security" className="uai-cv-auto mt-10 sm:mt-12">
          <Card className="rounded-2xl border-border bg-card/40">
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Shield className="h-5 w-5 text-success" />
                {t(locale, "landing.section.security.title")}
              </CardTitle>
              <CardDescription>{t(locale, "landing.section.security.desc")}</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-3 text-sm text-muted-foreground sm:grid-cols-3">
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="font-medium text-foreground">{t(locale, "landing.security.email.title")}</div>
                <div className="mt-1">{t(locale, "landing.security.email.desc")}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="font-medium text-foreground">{t(locale, "landing.security.role.title")}</div>
                <div className="mt-1">{t(locale, "landing.security.role.desc")}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="font-medium text-foreground">{t(locale, "landing.security.gateway.title")}</div>
                <div className="mt-1">{t(locale, "landing.security.gateway.desc")}</div>
              </div>
            </CardContent>
          </Card>
        </section>

        <section id="docs" className="uai-cv-auto mt-10 sm:mt-12">
          <Card className="rounded-2xl border-border bg-card/40">
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <BookOpen className="h-5 w-5 text-muted-foreground" />
                {t(locale, "landing.section.docs.title")}
              </CardTitle>
              <CardDescription>{t(locale, "landing.section.docs.desc")}</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="text-sm text-muted-foreground">
                {t(locale, "landing.section.docs.body")}
              </div>
              <IntentLink href="/dashboard" variant="outline" className="rounded-xl bg-transparent">
                {t(locale, "landing.cta.openConsole")}
                <ArrowRight className="h-4 w-4" />
              </IntentLink>
            </CardContent>
          </Card>
        </section>

        <footer className="uai-cv-auto mt-10 border-t border-border pt-8 text-xs text-muted-foreground">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="space-y-1">
              <div>© {new Date().getUTCFullYear()} {appName}</div>
              <div className="font-mono">{t(locale, "landing.footer.stack")}</div>
            </div>

            <div className="flex flex-col gap-2 sm:items-end">
              <div className="flex flex-wrap items-center gap-3">
                <Link
                  href="/terms"
                  className="transition-colors hover:text-foreground hover:underline underline-offset-4"
                >
                  {t(locale, "landing.footer.terms")}
                </Link>
                <span className="h-3 w-px bg-border/70" aria-hidden="true" />
                <Link
                  href="/privacy"
                  className="transition-colors hover:text-foreground hover:underline underline-offset-4"
                >
                  {t(locale, "landing.footer.privacy")}
                </Link>
              </div>
              <a
                href="mailto:support@0-0.pro"
                className="inline-flex items-center gap-2 font-mono transition-colors hover:text-foreground"
              >
                <Mail className="h-4 w-4 text-muted-foreground" />
                <span className="text-muted-foreground">{t(locale, "landing.footer.support")}:</span>
                <span>support@0-0.pro</span>
              </a>
            </div>
          </div>
        </footer>
      </main>
    </div>
  );
}
