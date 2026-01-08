import Link from "next/link";
import { cookies } from "next/headers";
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
  Zap
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { SpotlightCard } from "@/components/landing/spotlight-card";
import { cn } from "@/lib/utils";
import { isLoggedInCookie, SESSION_COOKIE_NAME } from "@/lib/auth";

export default async function LandingPage() {
  const token = (await cookies()).get(SESSION_COOKIE_NAME)?.value;
  const hasSession = isLoggedInCookie(token);

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
            <div
              className={cn(
                "flex h-9 w-9 items-center justify-center rounded-xl",
                "bg-gradient-to-br from-primary to-primary/80 text-primary-foreground shadow-sm",
                "transition-transform duration-300 group-hover:scale-[1.04]",
                "shadow-[0_0_0_1px_oklch(var(--primary)/0.25),0_12px_30px_oklch(var(--primary)/0.18)]"
              )}
            >
              <span className="font-logo text-[10px] leading-none tracking-tight">UA</span>
            </div>
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-foreground">Uni API</div>
              <div className="truncate text-[11px] text-muted-foreground">Deep Indigo Dark</div>
            </div>
          </Link>

          <nav className="hidden items-center gap-6 text-sm text-muted-foreground md:flex">
            <a href="#features" className="transition-colors hover:text-foreground">
              Features
            </a>
            <a href="#security" className="transition-colors hover:text-foreground">
              Security
            </a>
            <a href="#docs" className="transition-colors hover:text-foreground">
              Docs
            </a>
          </nav>

          <div className="flex items-center gap-2">
            {hasSession ? (
              <Button
                asChild
                className={cn(
                  "rounded-xl",
                  "shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_12px_34px_oklch(var(--primary)/0.22),inset_0_1px_0_0_oklch(var(--foreground)/0.12)]"
                )}
              >
                <Link href="/dashboard">
                  Open dashboard
                  <ArrowRight className="h-4 w-4" />
                </Link>
              </Button>
            ) : (
              <>
                <Button
                  asChild
                  variant="outline"
                  className={cn(
                    "rounded-xl bg-transparent",
                    "border-border/80 hover:bg-background/40"
                  )}
                >
                  <Link href="/login">Sign in</Link>
                </Button>
                <Button
                  asChild
                  className={cn(
                    "rounded-xl",
                    "shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_12px_34px_oklch(var(--primary)/0.22),inset_0_1px_0_0_oklch(var(--foreground)/0.12)]",
                    "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)] hover:-translate-y-0.5 hover:shadow-[0_0_0_1px_oklch(var(--primary)/0.45),0_18px_48px_oklch(var(--primary)/0.26),inset_0_1px_0_0_oklch(var(--foreground)/0.14)]"
                  )}
                >
                  <Link href="/register">
                    Get started
                    <ArrowRight className="h-4 w-4" />
                  </Link>
                </Button>
              </>
            )}
          </div>
        </div>
      </header>

      <main className="relative mx-auto w-full max-w-6xl px-4 py-10 sm:px-6 sm:py-14">
        <section className="relative">
          <div className="pointer-events-none absolute -left-6 -top-10 hidden h-40 w-40 rounded-full bg-primary/10 blur-2xl lg:block" />
          <div className="pointer-events-none absolute -right-8 top-10 hidden h-52 w-52 rounded-full bg-primary/10 blur-3xl lg:block" />

          <div className="grid gap-8 lg:grid-cols-2 lg:items-center">
            <div className="space-y-7">
              <div className="inline-flex items-center gap-2 rounded-full border border-border bg-background/40 px-3 py-1 text-xs text-muted-foreground backdrop-blur">
                <Sparkles className="h-3.5 w-3.5 text-primary" />
                Premium developer console
              </div>

              <h1 className="text-balance text-4xl font-semibold tracking-tight text-foreground sm:text-5xl lg:text-6xl">
                Your LLM control center —{" "}
                <span className="bg-gradient-to-b from-foreground via-foreground/95 to-foreground/70 bg-clip-text text-transparent">
                  calm, fast,
                </span>{" "}
                and{" "}
                <span className="bg-gradient-to-r from-primary via-primary/70 to-primary bg-clip-text text-transparent">
                  observable
                </span>
                .
              </h1>

              <p className="text-pretty text-base leading-relaxed text-muted-foreground sm:text-lg">
                统一入口接入多家模型渠道，提供清晰的用量与费用视图，并记录每次请求的关键指标。即使不懂技术，也能一眼看懂“发生了什么、花了多少”。
              </p>

              <div className="grid gap-3 sm:grid-cols-3">
                <div className="rounded-xl border border-border bg-background/35 p-4 backdrop-blur">
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <BadgeCheck className="h-4 w-4 text-success" />
                    Keys
                  </div>
                  <div className="mt-2 text-sm font-medium text-foreground">安全访问凭证</div>
                  <div className="mt-1 text-xs text-muted-foreground">撤销/恢复 + 随时复制</div>
                </div>
                <div className="rounded-xl border border-border bg-background/35 p-4 backdrop-blur">
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <LineChart className="h-4 w-4 text-primary" />
                    Spend
                  </div>
                  <div className="mt-2 text-sm font-medium text-foreground">费用与趋势</div>
                  <div className="mt-1 text-xs text-muted-foreground">24h / 7d 一目了然</div>
                </div>
                <div className="rounded-xl border border-border bg-background/35 p-4 backdrop-blur">
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <ScrollText className="h-4 w-4 text-warning" />
                    Logs
                  </div>
                  <div className="mt-2 text-sm font-medium text-foreground">请求级日志</div>
                  <div className="mt-1 text-xs text-muted-foreground">TTFT / TPS / IP</div>
                </div>
              </div>

              <div className="flex flex-col gap-3 sm:flex-row">
                {hasSession ? (
                  <Button
                    asChild
                    className={cn(
                      "rounded-xl",
                      "shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_12px_34px_oklch(var(--primary)/0.22),inset_0_1px_0_0_oklch(var(--foreground)/0.12)]"
                    )}
                  >
                    <Link href="/dashboard">
                      Open dashboard
                      <ArrowRight className="h-4 w-4" />
                    </Link>
                  </Button>
                ) : (
                  <>
                    <Button
                      asChild
                      className={cn(
                        "rounded-xl",
                        "shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_12px_34px_oklch(var(--primary)/0.22),inset_0_1px_0_0_oklch(var(--foreground)/0.12)]"
                      )}
                    >
                      <Link href="/register">
                        Create account
                        <ArrowRight className="h-4 w-4" />
                      </Link>
                    </Button>
                    <Button
                      asChild
                      variant="outline"
                      className="rounded-xl bg-transparent border-border/80 hover:bg-background/40"
                    >
                      <Link href="/login">Sign in</Link>
                    </Button>
                  </>
                )}
              </div>
            </div>

            <SpotlightCard className="rounded-2xl">
              <CardHeader className="pb-4">
                <CardTitle className="flex items-center gap-2">
                  <Layers3 className="h-5 w-5 text-muted-foreground" />
                  A calm dashboard
                </CardTitle>
                <CardDescription>像桌面软件一样精确、克制、但有深度。</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid gap-3 sm:grid-cols-3">
                  <div className="rounded-xl border border-border bg-background/35 p-4">
                    <div className="text-xs text-muted-foreground">Spend (24h)</div>
                    <div className="mt-2 text-lg font-semibold tracking-tight text-foreground">$0.23</div>
                    <div className="mt-1 text-xs text-muted-foreground">stable</div>
                  </div>
                  <div className="rounded-xl border border-border bg-background/35 p-4">
                    <div className="text-xs text-muted-foreground">Requests</div>
                    <div className="mt-2 text-lg font-semibold tracking-tight text-foreground">128</div>
                    <div className="mt-1 text-xs text-muted-foreground">last 24h</div>
                  </div>
                  <div className="rounded-xl border border-border bg-background/35 p-4">
                    <div className="text-xs text-muted-foreground">Error rate</div>
                    <div className="mt-2 text-lg font-semibold tracking-tight text-foreground">0%</div>
                    <div className="mt-1 text-xs text-muted-foreground">healthy</div>
                  </div>
                </div>

                <div className="overflow-hidden rounded-xl border border-border bg-background/35">
                  <div className="flex items-center justify-between border-b border-border bg-background/40 px-3 py-2">
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <Zap className="h-4 w-4 text-primary" />
                      Request log
                    </div>
                    <div className="text-[11px] text-muted-foreground">TTFT / TPS / Cost</div>
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
                  <span>All traffic through one gateway.</span>
                  <span className="font-mono">/v1/*</span>
                </div>
              </CardContent>
            </SpotlightCard>
          </div>
        </section>

        <section id="features" className="mt-12 sm:mt-16">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <div className="text-xs font-mono tracking-widest text-muted-foreground">FEATURES</div>
              <h2 className="mt-2 text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
                Visibility without complexity
              </h2>
              <p className="mt-2 max-w-2xl text-sm leading-relaxed text-muted-foreground sm:text-base">
                给团队一个稳定的“事实来源”：用量、花费、请求日志、可用模型——让沟通更快，问题更少。
              </p>
            </div>
            <div className="hidden sm:block">
              <div className="rounded-full border border-border bg-background/35 px-3 py-1 text-xs text-muted-foreground backdrop-blur">
                Built on OKLCH tokens
              </div>
            </div>
          </div>

          <div className="mt-6 grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            <SpotlightCard>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <LineChart className="h-5 w-5 text-primary" />
                  Spend & usage
                </CardTitle>
                <CardDescription>把“花了多少”变成可读的趋势。</CardDescription>
              </CardHeader>
              <CardContent className="text-sm text-muted-foreground">
                24h 摘要、7 天趋势、Top models，一眼定位异常增长。
              </CardContent>
            </SpotlightCard>

            <SpotlightCard>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <KeyRound className="h-5 w-5 text-warning" />
                  Keys that feel safe
                </CardTitle>
                <CardDescription>可撤销、可恢复、可复制。</CardDescription>
              </CardHeader>
              <CardContent className="text-sm text-muted-foreground">
                列表默认掩码显示；需要时一键复制完整 Key；显示 Last used 与 Total spend。
              </CardContent>
            </SpotlightCard>

            <SpotlightCard className="md:col-span-2 lg:col-span-1">
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <ScrollText className="h-5 w-5 text-muted-foreground" />
                  Audit logs
                </CardTitle>
                <CardDescription>每次请求都能追溯。</CardDescription>
              </CardHeader>
              <CardContent className="text-sm text-muted-foreground">
                记录模型、输入输出 token、耗时、TTFT、TPS、花费与来源 IP。
              </CardContent>
            </SpotlightCard>
          </div>
        </section>

        <section id="security" className="mt-10 sm:mt-12">
          <Card className="rounded-2xl border-border bg-card/40">
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Shield className="h-5 w-5 text-success" />
                Security by default
              </CardTitle>
              <CardDescription>邮箱验证码、禁用用户、权限边界。</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-3 text-sm text-muted-foreground sm:grid-cols-3">
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="font-medium text-foreground">Email verification</div>
                <div className="mt-1">Resend 验证码注册，减少垃圾注册。</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="font-medium text-foreground">Role protection</div>
                <div className="mt-1">Owner 保护，管理员不可越权封禁/删除。</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="font-medium text-foreground">Gateway isolation</div>
                <div className="mt-1">上游 API Key 不返回前端，统一网关出口。</div>
              </div>
            </CardContent>
          </Card>
        </section>

        <section id="docs" className="mt-10 sm:mt-12">
          <Card className="rounded-2xl border-border bg-card/40">
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <BookOpen className="h-5 w-5 text-muted-foreground" />
                Next steps
              </CardTitle>
              <CardDescription>三步走：注册 → 创建 Key → 开始调用。</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="text-sm text-muted-foreground">
                管理员先配置 Channels 与 Model Config；然后团队成员创建 API Key 即可调用 `/v1/chat/completions`。
              </div>
              <Button asChild variant="outline" className="rounded-xl bg-transparent">
                <Link href={hasSession ? "/dashboard" : "/login"}>
                  {hasSession ? "Open console" : "Sign in"}
                  <ArrowRight className="h-4 w-4" />
                </Link>
              </Button>
            </CardContent>
          </Card>
        </section>

        <footer className="mt-10 border-t border-border pt-8 text-xs text-muted-foreground">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div>© {new Date().getUTCFullYear()} Uni API Console</div>
            <div className="font-mono">OKLCH · shadcn/ui · Next.js</div>
          </div>
        </footer>
      </main>
    </div>
  );
}
