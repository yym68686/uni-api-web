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

export default async function TermsPage() {
  const appName = getAppName();
  const locale = await getRequestLocale();
  const zh = isZh(locale);

  const toc = zh
    ? [
        { id: "intro", label: "1. 概述" },
        { id: "definitions", label: "2. 定义" },
        { id: "accounts", label: "3. 账号与安全" },
        { id: "keys", label: "4. API Keys 与访问" },
        { id: "billing", label: "5. Credits、充值与计费" },
        { id: "availability", label: "6. 服务可用性与支持" },
        { id: "content", label: "7. 你的内容" },
        { id: "acceptable", label: "8. 可接受使用" },
        { id: "third-party", label: "9. 第三方服务" },
        { id: "ip", label: "10. 知识产权" },
        { id: "termination", label: "11. 暂停与终止" },
        { id: "disclaimer", label: "12. 免责声明" },
        { id: "liability", label: "13. 责任限制" },
        { id: "indemnity", label: "14. 赔偿" },
        { id: "law", label: "15. 适用法律与争议" },
        { id: "changes", label: "16. 条款变更" },
        { id: "contact", label: "17. 联系我们" }
      ]
    : [
        { id: "intro", label: "1. Overview" },
        { id: "definitions", label: "2. Definitions" },
        { id: "accounts", label: "3. Accounts & Security" },
        { id: "keys", label: "4. API Keys & Access" },
        { id: "billing", label: "5. Credits, Billing & Refunds" },
        { id: "availability", label: "6. Service Availability & Support" },
        { id: "content", label: "7. Your Content" },
        { id: "acceptable", label: "8. Acceptable Use" },
        { id: "third-party", label: "9. Third-Party Services" },
        { id: "ip", label: "10. Intellectual Property" },
        { id: "termination", label: "11. Suspension & Termination" },
        { id: "disclaimer", label: "12. Disclaimer" },
        { id: "liability", label: "13. Limitation of Liability" },
        { id: "indemnity", label: "14. Indemnification" },
        { id: "law", label: "15. Governing Law & Disputes" },
        { id: "changes", label: "16. Changes to These Terms" },
        { id: "contact", label: "17. Contact" }
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
            <CardTitle className="text-2xl">{zh ? "服务条款" : "Terms of Service"}</CardTitle>
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
              <Section id="intro" title={zh ? "1. 概述" : "1. Overview"}>
                <p>
                  {zh
                    ? `欢迎使用 ${appName}（以下简称“本服务”）。本服务提供 LLM API 网关、控制台、API Key 管理、请求日志与计费等能力。`
                    : `Welcome to ${appName} (the “Service”). The Service provides an LLM API gateway, a web console, API key management, request logs, and billing features.`}
                </p>
                <p>
                  {zh
                    ? "通过创建账号、登录控制台或使用 API Key 调用接口，即表示你已阅读、理解并同意本条款与我们的隐私政策；若你不同意，请不要使用本服务。"
                    : "By creating an account, signing in, or using an API key, you agree to these Terms and our Privacy Policy. If you do not agree, do not use the Service."}
                </p>
                <p>
                  {zh
                    ? "若你代表某个组织或公司使用本服务，你声明你有权代表该主体接受本条款，且“你”也指该主体。"
                    : "If you are using the Service on behalf of an organization, you represent that you have authority to accept these Terms for that entity, and “you” refers to that entity."}
                </p>
              </Section>

              <Section id="definitions" title={zh ? "2. 定义" : "2. Definitions"}>
                <ul className="list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "“控制台”指你登录后用于管理密钥、模型与账单等功能的网页界面。"
                      : "“Console” means the web interface used to manage keys, models, billing, and related settings."}
                  </li>
                  <li>
                    {zh
                      ? "“API Key”指用于调用接口并代表你的账号进行鉴权的密钥。"
                      : "“API Key” means the credential used to authenticate API calls on behalf of your account."}
                  </li>
                  <li>
                    {zh
                      ? "“Credits”指你在本服务中用于计费与结算的充值额度（非现金、非存款、不可兑换）。"
                      : "“Credits” means the prepaid balance used for billing and settlement within the Service (not cash, not a deposit, and not redeemable)."}
                  </li>
                  <li>
                    {zh
                      ? "“上游提供方”指本服务为完成你的请求而转发到的第三方模型/推理服务提供方。"
                      : "“Upstream Provider” means the third-party model/inference provider to which we forward requests to fulfill API calls."}
                  </li>
                </ul>
              </Section>

              <Section id="accounts" title={zh ? "3. 账号与安全" : "3. Accounts & Security"}>
                <ul className="list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "你需要提供真实、准确的注册信息，并及时更新。"
                      : "You must provide accurate account information and keep it up to date."}
                  </li>
                  <li>
                    {zh
                      ? "你必须具备签订具有约束力合同的法定年龄与能力，并遵守适用法律。"
                      : "You must be legally able to enter into a binding contract and comply with applicable laws."}
                  </li>
                  <li>
                    {zh
                      ? "你对账号、会话与 API Key 的所有活动负责，包括由你授权或未授权的使用。"
                      : "You are responsible for all activity under your account, sessions, and API keys, whether authorized or not."}
                  </li>
                  <li>
                    {zh
                      ? "请妥善保管密钥与登录凭证，如发现异常请立即联系我们。"
                      : "Keep your credentials secure and contact us immediately if you suspect unauthorized access."}
                  </li>
                </ul>
              </Section>

              <Section id="keys" title={zh ? "4. API Keys 与访问" : "4. API Keys & Access"}>
                <ul className="list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "API Key 用于代表你的账号访问接口。请勿将密钥公开发布或嵌入到可被第三方获取的客户端代码中。"
                      : "API keys allow access to the API on your behalf. Do not publish keys publicly or embed them in client-side code that others can access."}
                  </li>
                  <li>
                    {zh
                      ? "如你怀疑密钥泄露，请立即在控制台中撤销并创建新密钥。"
                      : "If you suspect a key has been compromised, revoke it immediately in the Console and create a new one."}
                  </li>
                  <li>
                    {zh
                      ? "你不得尝试绕过鉴权、权限控制、计费或任何技术限制。"
                      : "You may not attempt to bypass authentication, authorization, billing, or any technical restrictions."}
                  </li>
                  <li>
                    {zh
                      ? "我们可能实施限流、配额或其它访问控制，以保护服务稳定性与安全。"
                      : "We may enforce rate limits, quotas, or other access controls to protect service reliability and security."}
                  </li>
                </ul>
              </Section>

              <Section id="billing" title={zh ? "5. Credits、充值与计费" : "5. Credits, Billing & Refunds"}>
                <p>
                  {zh
                    ? "你可以通过一次性支付为账号充值 Credits。支付由第三方支付平台处理；我们在收到并验证支付成功事件后，为你的账号入账对应的 Credits。"
                    : "You can top up Credits through a one-time payment. Payments are processed by a third-party provider; after we receive and verify a successful payment event, we credit your account accordingly."}
                </p>
                <p>
                  {zh
                    ? "Credits 仅用于本服务内的计费与结算，不构成法定货币、电子货币或存款，不可转让、不可兑换为现金（法律另有规定的除外）。"
                    : "Credits are used only for billing and settlement within the Service. Credits are not legal tender, e-money, or a deposit, and are not transferable or redeemable for cash (except as required by law)."}
                </p>
                <ul className="list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "为避免滥用与欺诈，某些接口可能要求你的账号余额为正数方可使用。"
                      : "To help prevent abuse and fraud, some endpoints may require a positive credit balance to use."}
                  </li>
                  <li>
                    {zh
                      ? "我们可能要求以 USD 结算并对充值金额设置最小/最大限制；实际可用规则以控制台提示为准。"
                      : "We may require USD settlement and enforce minimum/maximum top-up amounts. The Console will show the applicable limits."}
                  </li>
                  <li>
                    {zh
                      ? "除非法律另有要求，支付费用通常不可退款；如需退款支持，请联系支持邮箱。"
                      : "Unless required by law, payments are generally non-refundable. If you need help, contact support."}
                  </li>
                  <li>
                    {zh
                      ? "我们可能在未来调整计费策略、定价或充值规则，并在生效前以合理方式通知。"
                      : "We may change pricing or billing rules in the future and will provide reasonable notice before changes take effect."}
                  </li>
                  <li>
                    {zh
                      ? "如发生拒付（chargeback）或支付欺诈，我们可能撤销相应 Credits、暂停服务或采取其它必要措施。"
                      : "If a chargeback or payment fraud occurs, we may reverse corresponding Credits, suspend the Service, or take other necessary actions."}
                  </li>
                </ul>
              </Section>

              <Section
                id="availability"
                title={zh ? "6. 服务可用性与支持" : "6. Service Availability & Support"}
              >
                <p>
                  {zh
                    ? "我们会努力保持服务稳定运行，但不承诺提供任何 SLA 或持续可用性保证。我们可能因维护、升级、故障或第三方依赖不可用而中断或降低服务质量。"
                    : "We aim to keep the Service reliable, but we do not provide an SLA or guarantee uninterrupted availability. The Service may be interrupted or degraded due to maintenance, upgrades, incidents, or third-party dependencies."}
                </p>
                <p>
                  {zh
                    ? "你可以通过支持邮箱联系我们获取支持与故障协助。"
                    : "You can contact us via the support email for help and incident assistance."}
                </p>
              </Section>

              <Section id="content" title={zh ? "7. 你的内容" : "7. Your Content"}>
                <p>
                  {zh
                    ? "你通过本服务提交的请求内容（例如 prompts、messages）及其响应内容（例如 completions、responses）归你或相关权利人所有。"
                    : "You (or your licensors) retain ownership of the content you submit (e.g., prompts/messages) and the outputs you receive (e.g., completions/responses)."}
                </p>
                <p>
                  {zh
                    ? "你声明你拥有或已获得必要的权利与许可来提交内容并使用输出，并对内容与用途的合法性负责。"
                    : "You represent that you have the rights and permissions necessary to submit your content and use outputs, and you are responsible for the legality of your content and use."}
                </p>
                <p>
                  {zh
                    ? "为提供服务，我们需要处理并转发你的请求到配置的上游模型服务。我们默认不会将请求正文写入请求日志；但上游服务可能会根据其政策记录或处理你的内容。"
                    : "To provide the Service, we process and forward your requests to configured upstream model providers. We do not store request bodies in request logs by default; however, upstream providers may process or retain content under their own policies."}
                </p>
                <p>
                  {zh
                    ? "请避免在 prompts 中提交敏感个人信息、密钥或任何你不希望第三方处理的数据。"
                    : "Avoid submitting sensitive personal data, secrets, or anything you do not want third parties to process in prompts."}
                </p>
              </Section>

              <Section id="acceptable" title={zh ? "8. 可接受使用" : "8. Acceptable Use"}>
                <p>
                  {zh
                    ? "你不得使用本服务从事违法、侵权、欺诈、滥用或破坏性行为，包括但不限于："
                    : "You may not use the Service for unlawful, infringing, fraudulent, abusive, or harmful activity, including:"}
                </p>
                <ul className="list-disc space-y-2 pl-5">
                  <li>
                    {zh
                      ? "攻击、扫描或试图干扰我们的系统、网络与安全机制；"
                      : "Attacking, probing, or attempting to disrupt our systems, networks, or security controls;"}
                  </li>
                  <li>
                    {zh
                      ? "批量注册、滥用试用/充值规则、或绕过风控；"
                      : "Automating abuse (e.g., mass registrations) or attempting to bypass anti-fraud controls;"}
                  </li>
                  <li>
                    {zh
                      ? "传播恶意软件、钓鱼内容或其它有害内容。"
                      : "Distributing malware, phishing, or other harmful content."}
                  </li>
                  <li>
                    {zh
                      ? "侵犯他人隐私或知识产权，或传播违法违规内容。"
                      : "Violating privacy or intellectual property rights, or distributing illegal content."}
                  </li>
                  <li>
                    {zh
                      ? "利用本服务对第三方实施骚扰、诈骗、垃圾信息投递或其他滥用行为。"
                      : "Using the Service to harass, scam, spam, or otherwise abuse others."}
                  </li>
                </ul>
              </Section>

              <Section id="third-party" title={zh ? "9. 第三方服务" : "9. Third-Party Services"}>
                <p>
                  {zh
                    ? "本服务可能集成或依赖第三方服务（例如：Google OAuth 登录、邮件服务、支付平台、上游模型提供方）。你使用相关能力时，也应遵守第三方的条款与政策。"
                    : "The Service integrates with or depends on third-party services (e.g., Google OAuth, email delivery, payments, and upstream model providers). Your use of those services may be subject to their terms and policies."}
                </p>
                <p>
                  {zh
                    ? "上游提供方的可用性、输出质量、合规要求与数据处理方式由其自行决定。你应确保你的使用符合上游提供方的政策与适用法律。"
                    : "Upstream Providers control their availability, output quality, compliance requirements, and data handling. You are responsible for ensuring your use complies with their policies and applicable law."}
                </p>
              </Section>

              <Section id="ip" title={zh ? "10. 知识产权" : "10. Intellectual Property"}>
                <p>
                  {zh
                    ? "本服务及其相关的软件、界面、文档、商标与标识（不含你的内容）由我们或我们的许可方拥有。"
                    : "The Service and related software, UI, documentation, trademarks, and branding (excluding your content) are owned by us or our licensors."}
                </p>
                <p>
                  {zh
                    ? "在你遵守本条款的前提下，我们授予你一项有限的、非排他的、不可转让的许可，用于访问并使用本服务。"
                    : "Subject to your compliance with these Terms, we grant you a limited, non-exclusive, non-transferable license to access and use the Service."}
                </p>
              </Section>

              <Section id="termination" title={zh ? "11. 暂停与终止" : "11. Suspension & Termination"}>
                <p>
                  {zh
                    ? "如你违反本条款、存在安全风险或滥用行为，我们可能暂停或终止你对服务的访问（包括 API Key）。"
                    : "If you violate these Terms, pose a security risk, or abuse the Service, we may suspend or terminate your access (including API keys)."}
                </p>
                <p>
                  {zh
                    ? "你也可以在控制台中注销账号。注销会删除你的账号与相关数据（具体范围以产品实际功能为准）。"
                    : "You may also delete your account from the console. Deletion removes your account and related data (subject to product functionality and legal requirements)."}
                </p>
              </Section>

              <Section id="disclaimer" title={zh ? "12. 免责声明" : "12. Disclaimer"}>
                <p>
                  {zh
                    ? "本服务按“现状”与“可用”提供，我们不对服务的持续可用性、不间断性或无错误作出保证。"
                    : "The Service is provided “as is” and “as available”. We do not guarantee uninterrupted or error-free operation."}
                </p>
                <p>
                  {zh
                    ? "本服务可能转发并展示由上游模型生成的输出。模型输出可能不准确、不完整或具有误导性，你应自行评估并承担使用风险。"
                    : "The Service may forward and display outputs generated by upstream models. Outputs may be inaccurate, incomplete, or misleading. You are responsible for evaluating outputs and using them at your own risk."}
                </p>
              </Section>

              <Section id="liability" title={zh ? "13. 责任限制" : "13. Limitation of Liability"}>
                <p>
                  {zh
                    ? "在法律允许的最大范围内，我们不对间接、附带、特殊、后果性或惩罚性损害承担责任。"
                    : "To the maximum extent permitted by law, we are not liable for indirect, incidental, special, consequential, or punitive damages."}
                </p>
                <p>
                  {zh
                    ? "在法律允许的最大范围内，我们对任何索赔的累计责任不超过你在引发索赔事件前的 12 个月内就本服务已支付的费用总额。"
                    : "To the maximum extent permitted by law, our aggregate liability for any claim will not exceed the total fees you paid for the Service during the 12 months preceding the event giving rise to the claim."}
                </p>
                <p>
                  {zh
                    ? "某些司法辖区不允许对责任进行限制；如适用法律另有规定，应以适用法律为准。"
                    : "Some jurisdictions do not allow certain limitations; where prohibited, the limitations may not apply to you."}
                </p>
              </Section>

              <Section id="indemnity" title={zh ? "14. 赔偿" : "14. Indemnification"}>
                <p>
                  {zh
                    ? "你同意就因你使用本服务、你的内容、或你违反本条款与适用法律而引起的第三方索赔、损失与费用（包括合理的律师费），对我们及我们的关联方、员工进行赔偿并使其免受损害。"
                    : "You agree to indemnify and hold us and our affiliates, employees, and agents harmless from third-party claims, losses, and expenses (including reasonable attorneys’ fees) arising from your use of the Service, your content, or your violation of these Terms or applicable law."}
                </p>
              </Section>

              <Section id="law" title={zh ? "15. 适用法律与争议" : "15. Governing Law & Disputes"}>
                <p>
                  {zh
                    ? "本条款受适用法律管辖。若你与我们之间产生争议，我们建议你先通过支持邮箱与我们联系以寻求友好解决。"
                    : "These Terms are governed by applicable law. If a dispute arises, please contact us first so we can try to resolve it informally."}
                </p>
              </Section>

              <Section id="changes" title={zh ? "16. 条款变更" : "16. Changes to These Terms"}>
                <p>
                  {zh
                    ? "我们可能不时更新本条款。更新后我们会修改页面上的“最后更新”日期，并可能通过站内提示等方式通知。你在条款更新后继续使用服务，即表示你接受更新后的条款。"
                    : "We may update these Terms from time to time. We will update the “Last updated” date and may provide notice via the Service. Continued use after changes become effective means you accept the updated Terms."}
                </p>
              </Section>

              <Section id="contact" title={zh ? "17. 联系我们" : "17. Contact"}>
                <p>
                  {zh
                    ? "如果你对本条款有任何疑问，请通过以下邮箱联系我们："
                    : "If you have questions about these Terms, contact us at:"}
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
