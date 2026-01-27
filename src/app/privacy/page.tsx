import { ArrowLeft, Mail } from "lucide-react";
import Link from "next/link";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { getAppName } from "@/lib/app-config";
import { getRequestLocale } from "@/lib/i18n/server";
import { t, type Locale } from "@/lib/i18n/messages";

const SUPPORT_EMAIL = "support@0-0.pro";
const LAST_UPDATED = "2026-01-27";

function isZh(locale: Locale) {
  return locale === "zh-CN";
}

function Section({
  id,
  title,
  children
}: {
  id: string;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section id={id} className="scroll-mt-24">
      <h2 className="text-base font-semibold tracking-tight text-foreground sm:text-lg">
        {title}
      </h2>
      <div className="mt-3 space-y-3 text-sm leading-relaxed text-muted-foreground">
        {children}
      </div>
    </section>
  );
}

export default async function PrivacyPage() {
  const appName = getAppName();
  const locale = await getRequestLocale();
  const zh = isZh(locale);

  const toc = zh
    ? [
        { id: "overview", label: "1. 概述" },
        { id: "collect", label: "2. 我们收集哪些信息" },
        { id: "use", label: "3. 我们如何使用信息" },
        { id: "share", label: "4. 我们如何共享信息" },
        { id: "api-content", label: "5. API 内容与上游提供方" },
        { id: "cookies", label: "6. Cookies 与本地存储" },
        { id: "retention", label: "7. 数据保留" },
        { id: "security", label: "8. 安全" },
        { id: "transfers", label: "9. 跨境传输" },
        { id: "rights", label: "10. 你的权利与选择" },
        { id: "children", label: "11. 未成年人" },
        { id: "changes", label: "12. 政策变更" },
        { id: "contact", label: "13. 联系我们" }
      ]
    : [
        { id: "overview", label: "1. Overview" },
        { id: "collect", label: "2. Information We Collect" },
        { id: "use", label: "3. How We Use Information" },
        { id: "share", label: "4. How We Share Information" },
        { id: "api-content", label: "5. API Content & Upstream Providers" },
        { id: "cookies", label: "6. Cookies & Local Storage" },
        { id: "retention", label: "7. Data Retention" },
        { id: "security", label: "8. Security" },
        { id: "transfers", label: "9. International Transfers" },
        { id: "rights", label: "10. Your Rights & Choices" },
        { id: "children", label: "11. Children" },
        { id: "changes", label: "12. Changes to This Policy" },
        { id: "contact", label: "13. Contact" }
      ];

  return (
    <div className="relative min-h-dvh overflow-hidden bg-background">
      <div className="pointer-events-none absolute inset-0 uai-landing-canvas" />
      <div className="pointer-events-none absolute inset-0 uai-landing-grid-64 opacity-30" />
      <div className="pointer-events-none absolute inset-0 uai-landing-noise" />

      <header className="sticky top-0 z-40 border-b border-border bg-background/70 backdrop-blur-xl">
        <div className="mx-auto flex h-14 max-w-4xl items-center justify-between gap-3 px-4 sm:px-6">
          <Link
            href="/"
            className="inline-flex items-center gap-2 text-sm text-muted-foreground transition-colors hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" />
            {t(locale, "common.backHome")}
          </Link>
          <div className="font-logo text-sm tracking-tight text-foreground">{appName}</div>
        </div>
      </header>

      <main className="relative mx-auto w-full max-w-4xl px-4 py-10 sm:px-6 sm:py-14">
        <Card
          className={cn(
            "overflow-hidden rounded-2xl border-border bg-card/40 backdrop-blur-xl",
            "shadow-[0_0_0_1px_oklch(var(--border)/0.6),0_14px_40px_oklch(0%_0_0/0.35)]"
          )}
        >
          <CardHeader>
            <CardTitle className="text-2xl">{zh ? "隐私政策" : "Privacy Policy"}</CardTitle>
            <CardDescription className="flex flex-wrap items-center gap-2">
              <span>{zh ? "最后更新" : "Last updated"}:</span>
              <span className="font-mono">{LAST_UPDATED}</span>
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-8">
            <div className="rounded-xl border border-border bg-background/35 p-4">
              <div className="text-sm font-medium text-foreground">{zh ? "目录" : "Contents"}</div>
              <ul className="mt-3 grid gap-2 text-sm text-muted-foreground sm:grid-cols-2">
                {toc.map((item) => (
                  <li key={item.id}>
                    <a
                      href={`#${item.id}`}
                      className="inline-flex items-center gap-2 transition-colors hover:text-foreground hover:underline underline-offset-4"
                    >
                      <span className="font-mono text-xs text-muted-foreground">#</span>
                      {item.label}
                    </a>
                  </li>
                ))}
              </ul>
            </div>

            <article className="space-y-10">
              <Section id="overview" title={zh ? "1. 概述" : "1. Overview"}>
                <p>
                  {zh
                    ? `本隐私政策说明 ${appName} 如何收集、使用与共享你的信息，以及你拥有的选择与权利。`
                    : `This Privacy Policy explains how ${appName} collects, uses, and shares information, and the choices you have.`}
                </p>
                <p>
                  {zh
                    ? "我们尽量减少数据收集：默认情况下，我们的请求日志只记录请求元数据与用量信息，不记录 prompts 或响应正文。"
                    : "We aim to minimize data collection: by default, our request logs record metadata and usage, not prompt or response bodies."}
                </p>
                <p>
                  {zh
                    ? `如对本政策有疑问，你可以通过 ${SUPPORT_EMAIL} 联系我们。`
                    : `If you have questions about this Policy, contact us at ${SUPPORT_EMAIL}.`}
                </p>
              </Section>

              <Section id="collect" title={zh ? "2. 我们收集哪些信息" : "2. Information We Collect"}>
                <ul className="list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "账号信息：邮箱、登录方式（密码或 OAuth）、组织/角色/分组信息、最后登录时间等。"
                      : "Account information: email, sign-in method (password or OAuth), organization/role/group, etc."}
                  </li>
                  <li>
                    {zh
                      ? "使用数据（请求日志）：模型名称、时间、输入/缓存/输出 tokens、耗时、首字延迟、吞吐、花费估算、状态码、来源 IP 等。"
                      : "Usage data (request logs): model, timestamps, input/cached/output tokens, latency, TTFT, throughput, estimated cost, status code, and source IP."}
                  </li>
                  <li>
                    {zh
                      ? "计费与支付事件：充值金额、币种、支付状态，以及来自支付平台的事件回调内容（用于对账与幂等处理）。"
                      : "Billing and payment events: top-up amount, currency, payment status, and webhook payloads from the payment provider (for reconciliation and idempotency)."}
                  </li>
                  <li>
                    {zh
                      ? "设备与技术信息：浏览器类型、基础的诊断信息（例如错误日志/性能指标），用于排查问题与提升稳定性。"
                      : "Device and technical information: browser type and basic diagnostics (e.g., error logs/performance metrics) to improve reliability."}
                  </li>
                  <li>
                    {zh
                      ? "支持与沟通：你通过邮件与我们沟通时提供的内容。"
                      : "Support communications: content you provide when you contact us (e.g., via email)."}
                  </li>
                </ul>
              </Section>

              <Section id="use" title={zh ? "3. 我们如何使用信息" : "3. How We Use Information"}>
                <ul className="list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "提供与维护服务：登录鉴权、API 路由、渠道配置与访问控制等。"
                      : "Provide and operate the Service: authentication, API routing, channel configuration, and access control."}
                  </li>
                  <li>
                    {zh
                      ? "安全与风控：检测滥用、欺诈、异常访问与攻击。"
                      : "Security and fraud prevention: detect abuse, fraud, unusual access, and attacks."}
                  </li>
                  <li>
                    {zh
                      ? "产品与运营：统计分析、性能优化、故障排查与体验改进。"
                      : "Product and operations: analytics, performance improvements, debugging, and UX enhancements."}
                  </li>
                  <li>
                    {zh
                      ? "计费与对账：处理充值、核对支付状态、生成账单记录。"
                      : "Billing and reconciliation: process top-ups, verify payment status, and maintain billing records."}
                  </li>
                  <li>
                    {zh
                      ? "沟通：发送必要的事务性邮件（例如登录验证码、邮箱验证），并响应你的支持请求。"
                      : "Communications: send necessary transactional emails (e.g., login codes, email verification) and respond to support requests."}
                  </li>
                </ul>
              </Section>

              <Section id="share" title={zh ? "4. 我们如何共享信息" : "4. How We Share Information"}>
                <p>
                  {zh
                    ? "我们不会出售你的个人信息。我们可能在以下场景共享信息："
                    : "We do not sell your personal information. We may share information in these cases:"}
                </p>
                <ul className="list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "上游模型服务：为完成你的 API 请求，我们会将请求转发到管理员配置的上游模型提供方。"
                      : "Upstream model providers: to fulfill API requests, we forward requests to configured providers."}
                  </li>
                  <li>
                    {zh
                      ? "服务供应商：用于邮件发送（例如验证码）、支付处理（Creem）等。"
                      : "Service providers: email delivery (e.g., verification codes), payment processing (Creem), etc."}
                  </li>
                  <li>
                    {zh
                      ? "业务变更：如发生合并、收购、资产转让等，我们可能在法律允许的范围内转移信息。"
                      : "Business transfers: if we are involved in a merger, acquisition, or asset sale, information may be transferred as permitted by law."}
                  </li>
                  <li>
                    {zh
                      ? "法律与合规：为遵守法律、执行条款、或保护我们与他人的权利与安全。"
                      : "Legal and compliance: to comply with law, enforce terms, or protect rights and safety."}
                  </li>
                </ul>
              </Section>

              <Section id="api-content" title={zh ? "5. API 内容与上游提供方" : "5. API Content & Upstream Providers"}>
                <p>
                  {zh
                    ? "当你调用 API 时，请求内容会被转发给上游提供方以生成响应。上游提供方可能会根据其政策处理或保留内容。"
                    : "When you call the API, request content is forwarded to Upstream Providers to generate responses. Upstream Providers may process or retain content under their own policies."}
                </p>
                <p>
                  {zh
                    ? "默认情况下，我们不会在请求日志中记录 prompts 或响应正文，仅记录用于计费与排障所需的元数据与用量信息。"
                    : "By default, we do not store prompt or response bodies in request logs, and we only record metadata and usage needed for billing and diagnostics."}
                </p>
              </Section>

              <Section id="cookies" title={zh ? "6. Cookies 与本地存储" : "6. Cookies & Local Storage"}>
                <p>
                  {zh
                    ? "我们使用 Cookies 与本地存储来提供登录态与偏好设置，例如：会话 Cookie（uai_session）、语言偏好（uai_locale）、主题偏好等。"
                    : "We use cookies and local storage for authentication and preferences, such as the session cookie (uai_session), language preference (uai_locale), and theme preference."}
                </p>
                <ul className="mt-3 list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "必要 Cookies：用于保持登录态与安全防护（例如会话）。"
                      : "Necessary cookies: to keep you signed in and protect security (e.g., sessions)."}
                  </li>
                  <li>
                    {zh
                      ? "偏好设置：用于记住语言与主题等设置（例如 uai_locale）。"
                      : "Preferences: to remember settings like language and theme (e.g., uai_locale)."}
                  </li>
                </ul>
              </Section>

              <Section id="retention" title={zh ? "7. 数据保留" : "7. Data Retention"}>
                <p>
                  {zh
                    ? "我们会在提供服务所需的期限内保留相关数据，并在你注销账号或提出合理请求时删除或匿名化（法律要求保留的除外）。"
                    : "We retain information for as long as necessary to provide the Service and delete or anonymize it when you delete your account or make a reasonable request, unless retention is required by law."}
                </p>
                <ul className="mt-3 list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "账号信息：在账号存续期间保留；注销后按合理期限删除或匿名化。"
                      : "Account data: kept while your account is active; deleted or anonymized within a reasonable period after deletion."}
                  </li>
                  <li>
                    {zh
                      ? "计费与支付事件：为对账与合规目的可能需要更长时间保留。"
                      : "Billing/payment events: may be retained longer for reconciliation and compliance."}
                  </li>
                </ul>
              </Section>

              <Section id="security" title={zh ? "8. 安全" : "8. Security"}>
                <p>
                  {zh
                    ? "我们采取合理的技术与组织措施保护数据安全，但任何系统都无法保证绝对安全。请你也保护好账号与 API Key。"
                    : "We take reasonable technical and organizational measures to protect data, but no system is 100% secure. Please also protect your account and API keys."}
                </p>
              </Section>

              <Section id="transfers" title={zh ? "9. 跨境传输" : "9. International Transfers"}>
                <p>
                  {zh
                    ? "由于我们可能使用位于不同国家/地区的服务供应商与上游提供方，你的信息可能会在你所在国家/地区以外被处理。我们将采取合理措施保护数据安全，并在必要时遵循适用的数据传输要求。"
                    : "Because we may use service providers and Upstream Providers located in different countries/regions, your information may be processed outside your jurisdiction. We take reasonable measures to protect data and follow applicable transfer requirements when needed."}
                </p>
              </Section>

              <Section id="rights" title={zh ? "10. 你的权利与选择" : "10. Your Rights & Choices"}>
                <ul className="list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "你可以在控制台中更新账号信息、管理密钥、注销账号。"
                      : "You can update your account details, manage keys, and delete your account from the console."}
                  </li>
                  <li>
                    {zh
                      ? "你可以通过邮件联系我们，提出访问、更正或删除个人信息的请求。"
                      : "You can contact us by email to request access, correction, or deletion of personal information."}
                  </li>
                </ul>
              </Section>

              <Section id="children" title={zh ? "11. 未成年人" : "11. Children"}>
                <p>
                  {zh
                    ? "本服务不面向未成年人。我们不会在知情的情况下收集未成年人的个人信息；如你认为有此类情况，请联系我们以便处理。"
                    : "The Service is not directed to children. We do not knowingly collect children’s personal information. If you believe this has occurred, contact us so we can address it."}
                </p>
              </Section>

              <Section id="changes" title={zh ? "12. 政策变更" : "12. Changes to This Policy"}>
                <p>
                  {zh
                    ? "我们可能不时更新本隐私政策。更新后会修改“最后更新”日期，并可能通过站内提示等方式通知。"
                    : "We may update this Policy from time to time. We will update the “Last updated” date and may provide notice via the Service."}
                </p>
              </Section>

              <Section id="contact" title={zh ? "13. 联系我们" : "13. Contact"}>
                <p>
                  {zh
                    ? "如对本隐私政策有疑问或请求，请联系："
                    : "If you have questions or requests about this Privacy Policy, contact us at:"}
                </p>
                <a
                  href={`mailto:${SUPPORT_EMAIL}`}
                  className="inline-flex items-center gap-2 font-mono text-foreground/90 transition-colors hover:text-foreground"
                >
                  <Mail className="h-4 w-4 text-muted-foreground" />
                  {SUPPORT_EMAIL}
                </a>
              </Section>
            </article>
          </CardContent>
        </Card>
      </main>
    </div>
  );
}
