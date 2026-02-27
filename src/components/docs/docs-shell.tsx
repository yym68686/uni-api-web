import Link from "next/link";
import { ArrowLeft, ArrowRight, FileText } from "lucide-react";

import { BrandWordmark } from "@/components/brand/wordmark";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { getAppName } from "@/lib/app-config";
import { t, type Locale } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";
import { getDocsAlternates, getDocsNav, getDocsPrevNext, type DocsPage } from "@/content/docs/registry";

interface DocsShellProps {
  locale: Locale;
  page: DocsPage;
}

function DocsSection({ id, title, children }: { id: string; title: string; children: React.ReactNode }) {
  return (
    <section id={id} className="scroll-mt-24">
      <h2 className="text-base font-semibold tracking-tight text-foreground sm:text-lg">{title}</h2>
      <div className="mt-3 space-y-3 text-sm leading-relaxed text-muted-foreground">{children}</div>
    </section>
  );
}

function DocsLanguageSwitch({ locale, slug }: { locale: Locale; slug: readonly string[] }) {
  const alternates = getDocsAlternates(slug);
  const zhActive = locale === "zh-CN";
  const enActive = locale === "en";

  return (
    <div className="inline-flex items-center rounded-xl border border-border bg-background/40 p-1 text-xs backdrop-blur">
      <Link
        href={alternates["zh-CN"]}
        prefetch={false}
        className={cn(
          "rounded-lg px-2 py-1 transition-colors",
          zhActive ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground"
        )}
        aria-current={zhActive ? "page" : undefined}
      >
        中文
      </Link>
      <Link
        href={alternates.en}
        prefetch={false}
        className={cn(
          "rounded-lg px-2 py-1 transition-colors",
          enActive ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground"
        )}
        aria-current={enActive ? "page" : undefined}
      >
        EN
      </Link>
    </div>
  );
}

export function DocsShell({ locale, page }: DocsShellProps) {
  const appName = getAppName();
  const nav = getDocsNav(locale);
  const { prev, next } = getDocsPrevNext(locale, page.id);

  const headerTitle = t(locale, "docs.title");
  const consoleLabel = t(locale, "landing.openDashboard");

  return (
    <div className="relative min-h-dvh overflow-hidden bg-background">
      <div className="pointer-events-none absolute inset-0 uai-landing-canvas" />
      <div className="pointer-events-none absolute inset-0 uai-landing-grid-64 opacity-25" />
      <div className="pointer-events-none absolute inset-0 uai-landing-noise" />

      <header className="sticky top-0 z-40 border-b border-border bg-background/70 backdrop-blur-xl">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-3 px-4 sm:px-6">
          <div className="flex items-center gap-3">
            <Link
              href="/"
              className={cn(
                "inline-flex items-center gap-2 text-sm text-muted-foreground",
                "transition-colors hover:text-foreground"
              )}
            >
              <ArrowLeft suppressHydrationWarning className="h-4 w-4" />
              {t(locale, "common.backHome")}
            </Link>
            <Separator orientation="vertical" className="hidden h-5 sm:block" />
            <Link href="/" className="group hidden items-center gap-3 sm:flex">
              <BrandWordmark
                name={appName}
                className="text-sm transition-colors duration-200 group-hover:text-foreground"
              />
              <span className="text-sm text-muted-foreground">{headerTitle}</span>
            </Link>
          </div>

          <div className="flex items-center gap-2">
            <DocsLanguageSwitch locale={locale} slug={page.slug} />
            <Button asChild variant="outline" className="hidden rounded-xl sm:inline-flex">
              <Link href="/dashboard">{consoleLabel}</Link>
            </Button>
          </div>
        </div>
      </header>

      <main id="main" className="relative mx-auto w-full max-w-6xl px-4 py-10 sm:px-6 sm:py-14">
        <div className="mb-6 lg:hidden">
          <details className="rounded-xl border border-border bg-card/40 p-4 backdrop-blur">
            <summary className="cursor-pointer list-none text-sm font-medium text-foreground">
              {t(locale, "common.menu")}
            </summary>
            <div className="mt-4 space-y-4">
              {nav.map((group) => (
                <div key={group.id}>
                  <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    {group.title}
                  </div>
                  <div className="mt-2 grid gap-1">
                    {group.items.map((item) => {
                      const active = item.id === page.id;
                      return (
                        <Link
                          key={item.id}
                          href={item.href}
                          className={cn(
                            "rounded-lg px-2 py-1.5 text-sm",
                            active ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground"
                          )}
                          aria-current={active ? "page" : undefined}
                        >
                          {item.title}
                        </Link>
                      );
                    })}
                  </div>
                </div>
              ))}
            </div>
          </details>
        </div>

        <div className="grid gap-8 lg:grid-cols-[260px_1fr] lg:items-start">
          <aside className="hidden lg:block">
            <nav className="sticky top-20 space-y-6">
              {nav.map((group) => (
                <div key={group.id}>
                  <div className="px-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    {group.title}
                  </div>
                  <div className="mt-2 grid gap-1">
                    {group.items.map((item) => {
                      const active = item.id === page.id;
                      return (
                        <Link
                          key={item.id}
                          href={item.href}
                          className={cn(
                            "rounded-lg px-2 py-1.5 text-sm transition-colors",
                            active
                              ? "bg-muted text-foreground"
                              : "text-muted-foreground hover:bg-muted/40 hover:text-foreground"
                          )}
                          aria-current={active ? "page" : undefined}
                        >
                          {item.title}
                        </Link>
                      );
                    })}
                  </div>
                </div>
              ))}
            </nav>
          </aside>

          <div className="min-w-0 space-y-8">
            <header className="space-y-2">
              <div className="inline-flex items-center gap-2 text-xs text-muted-foreground">
                <FileText suppressHydrationWarning className="h-4 w-4" />
                <span>{headerTitle}</span>
              </div>
              <h1 className="text-balance text-3xl font-semibold tracking-tight text-foreground sm:text-4xl">
                {page.title}
              </h1>
              {page.description ? (
                <p className="text-pretty text-sm leading-relaxed text-muted-foreground sm:text-base">
                  {page.description}
                </p>
              ) : null}
            </header>

            {page.sections.length > 1 ? (
              <Card className="rounded-2xl bg-card/40 backdrop-blur">
                <CardHeader className="pb-3">
                  <CardTitle className="text-sm">{t(locale, "docs.onThisPage")}</CardTitle>
                </CardHeader>
                <CardContent className="pt-0">
                  <ul className="grid gap-2 text-sm text-muted-foreground sm:grid-cols-2">
                    {page.sections.map((section) => (
                      <li key={section.id}>
                        <a
                          href={`#${section.id}`}
                          className="underline-offset-4 hover:text-foreground hover:underline"
                        >
                          {section.title}
                        </a>
                      </li>
                    ))}
                  </ul>
                </CardContent>
              </Card>
            ) : null}

            <div className="space-y-10">
              {page.sections.map((section) => (
                <DocsSection key={section.id} id={section.id} title={section.title}>
                  {section.content}
                </DocsSection>
              ))}
            </div>

            {prev || next ? (
              <div className="grid gap-3 sm:grid-cols-2">
                {prev ? (
                  <Link
                    href={prev.href}
                    className={cn(
                      "rounded-2xl border border-border bg-card/40 p-4 backdrop-blur",
                      "transition-all duration-300 [transition-timing-function:var(--uai-expo-out)] hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-lg"
                    )}
                  >
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <ArrowLeft suppressHydrationWarning className="h-4 w-4" />
                      {t(locale, "docs.prev")}
                    </div>
                    <div className="mt-2 text-sm font-medium text-foreground">{prev.title}</div>
                  </Link>
                ) : (
                  <div />
                )}
                {next ? (
                  <Link
                    href={next.href}
                    className={cn(
                      "rounded-2xl border border-border bg-card/40 p-4 backdrop-blur",
                      "transition-all duration-300 [transition-timing-function:var(--uai-expo-out)] hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-lg"
                    )}
                  >
                    <div className="flex items-center justify-end gap-2 text-xs text-muted-foreground">
                      {t(locale, "docs.next")}
                      <ArrowRight suppressHydrationWarning className="h-4 w-4" />
                    </div>
                    <div className="mt-2 text-right text-sm font-medium text-foreground">{next.title}</div>
                  </Link>
                ) : null}
              </div>
            ) : null}
          </div>
        </div>
      </main>
    </div>
  );
}
