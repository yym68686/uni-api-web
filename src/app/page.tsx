import Link from "next/link";
import { cookies } from "next/headers";
import { ArrowRight, BadgeCheck, BookOpen, KeyRound, LineChart, ScrollText, Shield, Sparkles } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { isLoggedInCookie, SESSION_COOKIE_NAME } from "@/lib/auth";

export default async function LandingPage() {
  const token = (await cookies()).get(SESSION_COOKIE_NAME)?.value;
  const hasSession = isLoggedInCookie(token);

  return (
    <div className="min-h-dvh bg-background">
      <header className="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur-md">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-3 px-4 sm:px-6">
          <Link href="/" className="group flex items-center gap-3">
            <div
              className={cn(
                "flex h-9 w-9 items-center justify-center rounded-xl",
                "bg-gradient-to-br from-primary to-primary/80 text-primary-foreground shadow-sm",
                "transition-transform duration-300 group-hover:scale-[1.04]"
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
              <Button asChild className="rounded-xl">
                <Link href="/dashboard">
                  Open dashboard
                  <ArrowRight className="h-4 w-4" />
                </Link>
              </Button>
            ) : (
              <>
                <Button asChild variant="outline" className="rounded-xl bg-transparent">
                  <Link href="/login">Sign in</Link>
                </Button>
                <Button asChild className="rounded-xl shadow-[0_0_0_1px_oklch(var(--primary)/0.25),0_12px_30px_oklch(var(--primary)/0.22)] hover:shadow-[0_0_0_1px_oklch(var(--primary)/0.35),0_16px_40px_oklch(var(--primary)/0.28)]">
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

      <main className="mx-auto w-full max-w-6xl px-4 py-10 sm:px-6 sm:py-14">
        <section className="relative overflow-hidden rounded-2xl border border-border bg-card/40 p-6 sm:p-10">
          <div className="pointer-events-none absolute inset-0 uai-brand-grid opacity-70" />
          <div className="pointer-events-none absolute left-[12%] top-1/2 h-[520px] w-[520px] -translate-y-1/2 rounded-full uai-orb opacity-70" />

          <div className="relative grid gap-8 lg:grid-cols-2 lg:items-center">
            <div className="space-y-6">
              <div className="inline-flex items-center gap-2 rounded-full border border-border bg-background/40 px-3 py-1 text-xs text-muted-foreground">
                <Sparkles className="h-3.5 w-3.5 text-primary" />
                Unified LLM API Gateway
              </div>

              <h1 className="text-balance text-3xl font-semibold tracking-tight text-foreground sm:text-4xl">
                One console to route, observe, and bill your LLM traffic.
              </h1>

              <p className="text-pretty text-sm leading-relaxed text-muted-foreground sm:text-base">
                按用户组路由多渠道，统一模型开关与定价，实时统计用量与消费，并提供请求级日志。
              </p>

              <div className="flex flex-col gap-2 text-sm text-muted-foreground">
                <div className="flex items-center gap-2">
                  <BadgeCheck className="h-4 w-4 text-success" />
                  API Keys + revoke/restore + permanent copy
                </div>
                <div className="flex items-center gap-2">
                  <LineChart className="h-4 w-4 text-primary" />
                  Usage & spend analytics (24h / 7d)
                </div>
                <div className="flex items-center gap-2">
                  <ScrollText className="h-4 w-4 text-warning" />
                  Request logs (TTFT / TPS / cost / IP)
                </div>
              </div>

              <div className="flex flex-col gap-3 sm:flex-row">
                {hasSession ? (
                  <Button asChild className="rounded-xl">
                    <Link href="/dashboard">
                      Open dashboard
                      <ArrowRight className="h-4 w-4" />
                    </Link>
                  </Button>
                ) : (
                  <>
                    <Button asChild className="rounded-xl">
                      <Link href="/register">
                        Create account
                        <ArrowRight className="h-4 w-4" />
                      </Link>
                    </Button>
                    <Button asChild variant="outline" className="rounded-xl bg-transparent">
                      <Link href="/login">Sign in</Link>
                    </Button>
                  </>
                )}
              </div>
            </div>

            <Card className="relative overflow-hidden rounded-2xl border-border bg-background/30">
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <KeyRound className="h-5 w-5 text-muted-foreground" />
                  Quick start
                </CardTitle>
                <CardDescription>Use your API key to call the gateway.</CardDescription>
              </CardHeader>
              <CardContent>
                <pre className="scrollbar-hide overflow-auto rounded-xl border border-border bg-background/40 p-4 text-xs text-muted-foreground">
                  <code className="font-mono">{`curl -X POST http://localhost:3000/v1/chat/completions \\
  -H 'Content-Type: application/json' \\
  -H 'Authorization: Bearer sk-…' \\
  -d '{\"model\":\"gemini-2.5-flash\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}'`}</code>
                </pre>
                <div className="mt-4 flex items-center justify-between text-xs text-muted-foreground">
                  <span>Supports OpenAI-compatible routes.</span>
                  <span className="font-mono">/v1/*</span>
                </div>
              </CardContent>
            </Card>
          </div>
        </section>

        <section id="features" className="mt-10 grid gap-4 sm:mt-12 sm:grid-cols-2 lg:grid-cols-3">
          <Card className={cn("rounded-2xl border-border bg-card/40", "transition-all duration-300 hover:shadow-lg hover:scale-[1.02]")}>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <LineChart className="h-5 w-5 text-primary" />
                Analytics
              </CardTitle>
              <CardDescription>Spend、趋势与 Top models。</CardDescription>
            </CardHeader>
            <CardContent className="text-sm text-muted-foreground">
              通过 usage events 聚合 KPI，支持按天趋势展示，为计费/告警打基础。
            </CardContent>
          </Card>

          <Card className={cn("rounded-2xl border-border bg-card/40", "transition-all duration-300 hover:shadow-lg hover:scale-[1.02]")}>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <KeyRound className="h-5 w-5 text-warning" />
                API Keys
              </CardTitle>
              <CardDescription>按需复制、撤销与恢复。</CardDescription>
            </CardHeader>
            <CardContent className="text-sm text-muted-foreground">
              Key 列表默认掩码显示；需要时一键复制完整 Key；支持 revoke/restore 与删除。
            </CardContent>
          </Card>

          <Card className={cn("rounded-2xl border-border bg-card/40", "transition-all duration-300 hover:shadow-lg hover:scale-[1.02]")}>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <ScrollText className="h-5 w-5 text-muted-foreground" />
                Logs
              </CardTitle>
              <CardDescription>请求级耗时与性能指标。</CardDescription>
            </CardHeader>
            <CardContent className="text-sm text-muted-foreground">
              记录 TTFT、总耗时、TPS、花费与来源 IP，定位质量问题更快。
            </CardContent>
          </Card>
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
              <CardDescription>配置渠道与模型，然后开始调用。</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="text-sm text-muted-foreground">
                管理员先配置 Channels 与 Model Config；开发者创建 API Key 后即可调用 `/v1/chat/completions`。
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

