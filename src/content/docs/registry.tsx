import Link from "next/link";

import { CodeBlock } from "@/components/docs/code-block";
import { Badge } from "@/components/ui/badge";
import { type Locale, LOCALES } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils";

export interface DocsSection {
  id: string;
  title: string;
  content: React.ReactNode;
}

export interface DocsPage {
  id: string;
  slug: readonly string[];
  title: string;
  description?: string;
  sections: readonly DocsSection[];
}

export interface DocsNavItem {
  id: string;
  href: string;
  title: string;
  slug: readonly string[];
}

export interface DocsNavGroup {
  id: string;
  title: string;
  items: readonly DocsNavItem[];
}

interface DocsPageContent {
  title: string;
  description?: string;
  sections: readonly DocsSection[];
}

interface DocsPageDefinition {
  id: string;
  slug: readonly string[];
  groupId: string;
  content: Record<Locale, DocsPageContent>;
}

interface DocsNavGroupDefinition {
  id: string;
  title: Record<Locale, string>;
  pageIds: readonly string[];
}

function docsHref(locale: Locale, slug: readonly string[]) {
  const rest = slug.length > 0 ? `/${slug.join("/")}` : "";
  return `/docs/${locale}${rest}`;
}

export function parseDocsLocale(value: string): Locale | null {
  const normalized = value.trim();
  return (LOCALES as readonly string[]).includes(normalized)
    ? (normalized as Locale)
    : null;
}

const DOCS_GROUPS: readonly DocsNavGroupDefinition[] = [
  {
    id: "getting-started",
    title: { en: "Getting started", "zh-CN": "开始使用" },
    pageIds: ["overview", "quickstart"]
  },
  {
    id: "api",
    title: { en: "API", "zh-CN": "API" },
    pageIds: ["auth", "chat-completions", "responses"]
  },
  {
    id: "console",
    title: { en: "Console", "zh-CN": "控制台" },
    pageIds: ["console-keys", "console-logs", "console-billing"]
  },
  {
    id: "integrations",
    title: { en: "Integrations", "zh-CN": "集成" },
    pageIds: ["codex"]
  }
] as const;

const DOCS_PAGES: readonly DocsPageDefinition[] = [
  {
    id: "overview",
    slug: [],
    groupId: "getting-started",
    content: {
      en: {
        title: "Overview",
        description: "What this product is, and the fastest way to start calling the API.",
        sections: [
          {
            id: "what",
            title: "What is Uni API?",
            content: (
              <>
                <p>
                  Uni API is a gateway that gives your team a single API entrypoint across multiple upstream LLM
                  providers — with observability (logs, latency, tokens) and a clean Console for keys, models, and
                  billing.
                </p>
                <p>
                  If you already have an OpenAI-compatible client, you can usually switch Base URL and keep the rest
                  unchanged.
                </p>
              </>
            )
          },
          {
            id: "concepts",
            title: "Core concepts",
            content: (
              <ul className="list-disc pl-5">
                <li>
                  <span className="font-medium text-foreground">API Key</span>: the credential used for requests
                  (Bearer token).
                </li>
                <li>
                  <span className="font-medium text-foreground">Channels</span>: upstream connections configured by
                  admins.
                </li>
                <li>
                  <span className="font-medium text-foreground">Model config</span>: enable/disable models and set
                  prices per workspace/group.
                </li>
              </ul>
            )
          },
          {
            id: "next",
            title: "Next steps",
            content: (
              <div className="grid gap-3 sm:grid-cols-2">
                <Link
                  href="/docs/en/quickstart"
                  className={cn(
                    "rounded-xl border border-border bg-card/40 p-4 backdrop-blur",
                    "transition-all duration-300 [transition-timing-function:var(--uai-expo-out)] hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-lg"
                  )}
                >
                  <div className="text-sm font-medium text-foreground">Quickstart</div>
                  <div className="mt-1 text-xs text-muted-foreground">Register → create key → call the API.</div>
                </Link>
                <Link
                  href="/docs/en/api/chat-completions"
                  className={cn(
                    "rounded-xl border border-border bg-card/40 p-4 backdrop-blur",
                    "transition-all duration-300 [transition-timing-function:var(--uai-expo-out)] hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-lg"
                  )}
                >
                  <div className="text-sm font-medium text-foreground">Chat Completions</div>
                  <div className="mt-1 text-xs text-muted-foreground">OpenAI-compatible request/response format.</div>
                </Link>
              </div>
            )
          }
        ]
      },
      "zh-CN": {
        title: "概览",
        description: "产品是什么，以及最快开始调用 API 的方式。",
        sections: [
          {
            id: "what",
            title: "Uni API 是什么？",
            content: (
              <>
                <p>
                  Uni API 是一个 LLM API 网关：在多个上游提供方之间提供统一入口，并提供可观测性（日志、延迟、Tokens）以及用于
                  密钥、模型和账单的控制台。
                </p>
                <p>如果你已经在用 OpenAI 兼容的客户端，通常只需要替换 Base URL，其余保持不变。</p>
              </>
            )
          },
          {
            id: "concepts",
            title: "核心概念",
            content: (
              <ul className="list-disc pl-5">
                <li>
                  <span className="font-medium text-foreground">API 密钥</span>：用于请求鉴权的凭证（Bearer token）。
                </li>
                <li>
                  <span className="font-medium text-foreground">渠道</span>：管理员配置的上游连接。
                </li>
                <li>
                  <span className="font-medium text-foreground">模型配置</span>：按工作区/分组启用或禁用模型，并设置价格。
                </li>
              </ul>
            )
          },
          {
            id: "next",
            title: "下一步",
            content: (
              <div className="grid gap-3 sm:grid-cols-2">
                <Link
                  href="/docs/zh-CN/quickstart"
                  className={cn(
                    "rounded-xl border border-border bg-card/40 p-4 backdrop-blur",
                    "transition-all duration-300 [transition-timing-function:var(--uai-expo-out)] hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-lg"
                  )}
                >
                  <div className="text-sm font-medium text-foreground">快速开始</div>
                  <div className="mt-1 text-xs text-muted-foreground">注册 → 创建密钥 → 调用 API。</div>
                </Link>
                <Link
                  href="/docs/zh-CN/api/chat-completions"
                  className={cn(
                    "rounded-xl border border-border bg-card/40 p-4 backdrop-blur",
                    "transition-all duration-300 [transition-timing-function:var(--uai-expo-out)] hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-lg"
                  )}
                >
                  <div className="text-sm font-medium text-foreground">Chat Completions</div>
                  <div className="mt-1 text-xs text-muted-foreground">OpenAI 兼容的请求/响应格式。</div>
                </Link>
              </div>
            )
          }
        ]
      }
    }
  },
  {
    id: "quickstart",
    slug: ["quickstart"],
    groupId: "getting-started",
    content: {
      en: {
        title: "Quickstart",
        description: "From zero to your first request (with logs).",
        sections: [
          {
            id: "step-1",
            title: "1) Create an account",
            content: (
              <p>
                Register with email verification (or use Google OAuth if enabled). After you sign in, you’ll land on
                the Dashboard.
              </p>
            )
          },
          {
            id: "step-2",
            title: "2) Create an API key",
            content: (
              <p>
                Go to <Link className="text-primary underline-offset-4 hover:underline" href="/keys">API Keys</Link> and
                create a new key. Copy the Base URL from the page as well.
              </p>
            )
          },
          {
            id: "step-3",
            title: "3) Call /v1/chat/completions",
            content: (
              <CodeBlock
                lang="bash"
                code={[
                  "curl \"$BASE_URL/chat/completions\" \\",
                  "  -H \"Authorization: Bearer $API_KEY\" \\",
                  "  -H \"Content-Type: application/json\" \\",
                  "  -d '{",
                  "    \"model\": \"gpt-5.2\",",
                  "    \"messages\": [{\"role\":\"user\",\"content\":\"Hello\"}]",
                  "  }'"
                ].join("\n")}
              />
            )
          },
          {
            id: "step-4",
            title: "4) Verify logs",
            content: (
              <p>
                Open <Link className="text-primary underline-offset-4 hover:underline" href="/logs">Logs</Link>. You
                should see the request with tokens, latency, TTFT and cost.
              </p>
            )
          }
        ]
      },
      "zh-CN": {
        title: "快速开始",
        description: "从零到第一次请求（并在日志里看到它）。",
        sections: [
          {
            id: "step-1",
            title: "1）创建账号",
            content: <p>通过邮箱验证码注册（或在开启时使用 Google OAuth）。登录后会进入仪表盘。</p>
          },
          {
            id: "step-2",
            title: "2）创建 API 密钥",
            content: (
              <p>
                打开{" "}
                <Link className="text-primary underline-offset-4 hover:underline" href="/keys">
                  密钥
                </Link>{" "}
                页面创建新密钥，同时复制该页面展示的 Base URL。
              </p>
            )
          },
          {
            id: "step-3",
            title: "3）调用 /v1/chat/completions",
            content: (
              <CodeBlock
                lang="bash"
                code={[
                  "curl \"$BASE_URL/chat/completions\" \\",
                  "  -H \"Authorization: Bearer $API_KEY\" \\",
                  "  -H \"Content-Type: application/json\" \\",
                  "  -d '{",
                  "    \"model\": \"gpt-5.2\",",
                  "    \"messages\": [{\"role\":\"user\",\"content\":\"你好\"}]",
                  "  }'"
                ].join("\n")}
              />
            )
          },
          {
            id: "step-4",
            title: "4）查看日志",
            content: (
              <p>
                打开{" "}
                <Link className="text-primary underline-offset-4 hover:underline" href="/logs">
                  日志
                </Link>{" "}
                页面，你应该能看到该请求的 tokens、延迟、TTFT、费用等信息。
              </p>
            )
          }
        ]
      }
    }
  },
  {
    id: "auth",
    slug: ["api", "auth"],
    groupId: "api",
    content: {
      en: {
        title: "Authentication",
        description: "How to authenticate requests with API keys.",
        sections: [
          {
            id: "bearer",
            title: "Bearer token",
            content: (
              <>
                <p>Every API request must include an API key using the standard Bearer scheme.</p>
                <CodeBlock
                  lang="http"
                  code={["Authorization: Bearer <YOUR_API_KEY>"].join("\n")}
                />
              </>
            )
          },
          {
            id: "safety",
            title: "Safety tips",
            content: (
              <ul className="list-disc pl-5">
                <li>Do not ship keys to the browser or mobile apps.</li>
                <li>Use one key per environment (dev/staging/prod).</li>
                <li>Rotate keys if they were exposed.</li>
              </ul>
            )
          }
        ]
      },
      "zh-CN": {
        title: "鉴权",
        description: "如何使用 API 密钥为请求鉴权。",
        sections: [
          {
            id: "bearer",
            title: "Bearer token",
            content: (
              <>
                <p>所有 API 请求都需要通过标准 Bearer 方式携带密钥。</p>
                <CodeBlock lang="http" code={["Authorization: Bearer <YOUR_API_KEY>"].join("\n")} />
              </>
            )
          },
          {
            id: "safety",
            title: "安全建议",
            content: (
              <ul className="list-disc pl-5">
                <li>不要将密钥放到浏览器或移动端应用里。</li>
                <li>按环境拆分密钥（dev/staging/prod）。</li>
                <li>若泄露请及时轮换密钥。</li>
              </ul>
            )
          }
        ]
      }
    }
  },
  {
    id: "chat-completions",
    slug: ["api", "chat-completions"],
    groupId: "api",
    content: {
      en: {
        title: "Chat Completions",
        description: "OpenAI-compatible endpoint for chat-based text generation.",
        sections: [
          {
            id: "endpoint",
            title: "Endpoint",
            content: (
              <CodeBlock
                lang="http"
                code={["POST /v1/chat/completions", "Content-Type: application/json"].join("\n")}
              />
            )
          },
          {
            id: "request",
            title: "Request example",
            content: (
              <CodeBlock
                lang="json"
                code={JSON.stringify(
                  {
                    model: "gpt-5.2",
                    messages: [
                      { role: "system", content: "You are a helpful assistant." },
                      { role: "user", content: "Write a haiku about observability." }
                    ]
                  },
                  null,
                  2
                )}
              />
            )
          },
          {
            id: "streaming",
            title: "Streaming",
            content: (
              <>
                <p>
                  Set <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">stream: true</code>{" "}
                  to receive Server-Sent Events. Usage metrics may arrive at the end of the stream.
                </p>
                <div className="mt-3 rounded-xl border border-border bg-background/35 p-4 text-sm text-muted-foreground">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="secondary">Tip</Badge>
                    <span>Log pages show input / cached / output tokens when available.</span>
                  </div>
                </div>
              </>
            )
          }
        ]
      },
      "zh-CN": {
        title: "Chat Completions",
        description: "OpenAI 兼容的对话生成接口。",
        sections: [
          {
            id: "endpoint",
            title: "端点",
            content: (
              <CodeBlock
                lang="http"
                code={["POST /v1/chat/completions", "Content-Type: application/json"].join("\n")}
              />
            )
          },
          {
            id: "request",
            title: "请求示例",
            content: (
              <CodeBlock
                lang="json"
                code={JSON.stringify(
                  {
                    model: "gpt-5.2",
                    messages: [
                      { role: "system", content: "你是一个有帮助的助手。" },
                      { role: "user", content: "用一句话解释什么是可观测性。" }
                    ]
                  },
                  null,
                  2
                )}
              />
            )
          },
          {
            id: "streaming",
            title: "流式输出",
            content: (
              <>
                <p>
                  将{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">stream: true</code>{" "}
                  设置为 true 可获得 SSE 流式输出。Usage 统计可能在流结束时返回。
                </p>
                <div className="mt-3 rounded-xl border border-border bg-background/35 p-4 text-sm text-muted-foreground">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="secondary">提示</Badge>
                    <span>日志页面会在可用时展示 输入 / 缓存 / 输出 tokens。</span>
                  </div>
                </div>
              </>
            )
          }
        ]
      }
    }
  },
  {
    id: "responses",
    slug: ["api", "responses"],
    groupId: "api",
    content: {
      en: {
        title: "Responses (v1/responses)",
        description: "Pass-through endpoint compatible with upstream Responses API.",
        sections: [
          {
            id: "overview",
            title: "What it does",
            content: (
              <p>
                Uni API exposes <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">/v1/responses</code>{" "}
                and forwards the request body and headers upstream, while still enforcing your API key authentication and
                recording usage.
              </p>
            )
          },
          {
            id: "usage",
            title: "Usage accounting",
            content: (
              <ul className="list-disc pl-5">
                <li>
                  When upstream returns <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">cached_tokens</code>
                  , cached tokens are billed at 10% of the input price.
                </li>
                <li>Logs display Input / Cached / Output tokens when present.</li>
              </ul>
            )
          }
        ]
      },
      "zh-CN": {
        title: "Responses（v1/responses）",
        description: "与上游 Responses API 兼容的透传端点。",
        sections: [
          {
            id: "overview",
            title: "它做了什么",
            content: (
              <p>
                Uni API 提供{" "}
                <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">/v1/responses</code>{" "}
                端点，原样转发请求 body 与 headers 到上游；同时仍会执行本项目的 API key 鉴权并记录 usage。
              </p>
            )
          },
          {
            id: "usage",
            title: "Usage 计费",
            content: (
              <ul className="list-disc pl-5">
                <li>
                  当上游返回{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">cached_tokens</code>{" "}
                  时，缓存 tokens 按输入原价的 10% 计费。
                </li>
                <li>日志会在字段存在时展示 输入 / 缓存 / 输出 tokens。</li>
              </ul>
            )
          }
        ]
      }
    }
  },
  {
    id: "console-keys",
    slug: ["console", "keys"],
    groupId: "console",
    content: {
      en: {
        title: "API Keys (Console)",
        description: "Create, revoke and rotate keys from the web console.",
        sections: [
          {
            id: "create",
            title: "Create a key",
            content: (
              <p>
                Open <Link className="text-primary underline-offset-4 hover:underline" href="/keys">API Keys</Link> and
                create a new key. Keys are masked by default but can be copied in full at any time.
              </p>
            )
          }
        ]
      },
      "zh-CN": {
        title: "密钥（控制台）",
        description: "在控制台中创建、吊销与轮换密钥。",
        sections: [
          {
            id: "create",
            title: "创建密钥",
            content: (
              <p>
                打开{" "}
                <Link className="text-primary underline-offset-4 hover:underline" href="/keys">
                  密钥
                </Link>{" "}
                页面创建新密钥。密钥默认遮罩显示，但可随时复制完整密钥。
              </p>
            )
          }
        ]
      }
    }
  },
  {
    id: "console-logs",
    slug: ["console", "logs"],
    groupId: "console",
    content: {
      en: {
        title: "Logs (Console)",
        description: "Audit every request: tokens, latency, cost and source IP.",
        sections: [
          {
            id: "what",
            title: "What you can see",
            content: (
              <>
                <p>
                  Open <Link className="text-primary underline-offset-4 hover:underline" href="/logs">Logs</Link> to view
                  per-request metrics.
                </p>
                <ul className="mt-3 list-disc pl-5">
                  <li>Input / Cached / Output tokens</li>
                  <li>TTFT, TPS, total latency</li>
                  <li>Cost and source IP</li>
                </ul>
              </>
            )
          }
        ]
      },
      "zh-CN": {
        title: "日志（控制台）",
        description: "审计每次请求：tokens、延迟、费用与来源 IP。",
        sections: [
          {
            id: "what",
            title: "你能看到什么",
            content: (
              <>
                <p>
                  打开{" "}
                  <Link className="text-primary underline-offset-4 hover:underline" href="/logs">
                    日志
                  </Link>{" "}
                  查看每次请求的指标。
                </p>
                <ul className="mt-3 list-disc pl-5">
                  <li>输入 / 缓存 / 输出 tokens</li>
                  <li>TTFT、TPS、总延迟</li>
                  <li>费用与来源 IP</li>
                </ul>
              </>
            )
          }
        ]
      }
    }
  },
  {
    id: "console-billing",
    slug: ["console", "billing"],
    groupId: "console",
    content: {
      en: {
        title: "Billing (Console)",
        description: "Top up credits and review your balance history.",
        sections: [
          {
            id: "where",
            title: "Where to manage billing",
            content: (
              <p>
                Billing is available in the Console at{" "}
                <Link className="text-primary underline-offset-4 hover:underline" href="/billing">
                  Billing
                </Link>
                . The page shows balance history and your top-ups.
              </p>
            )
          }
        ]
      },
      "zh-CN": {
        title: "账单（控制台）",
        description: "充值 Credits 并查看余额变更历史。",
        sections: [
          {
            id: "where",
            title: "在哪里管理账单",
            content: (
              <p>
                账单页面位于控制台的{" "}
                <Link className="text-primary underline-offset-4 hover:underline" href="/billing">
                  账单
                </Link>
                。你可以在这里查看余额变更历史与充值记录。
              </p>
            )
          }
        ]
      }
    }
  },
  {
    id: "codex",
    slug: ["integrations", "codex"],
    groupId: "integrations",
    content: {
      en: {
        title: "Codex setup",
        description: "Configure Codex to use 0-0.pro as the model provider (wire_api = responses).",
        sections: [
          {
            id: "install",
            title: "1) Install Codex",
            content: (
              <p>
                Install Codex in your editor (VS Code). After installation, Codex will read its configuration from{" "}
                <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">~/.codex</code>.
              </p>
            )
          },
          {
            id: "config",
            title: "2) Update ~/.codex/config.toml",
            content: (
              <>
                <p>
                  Open{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">
                    ~/.codex/config.toml
                  </code>
                  . Below is a complete example you can copy. Replace the paths under{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">[projects.*]</code>{" "}
                  with your own.
                </p>
                <CodeBlock
                  lang="toml"
                  code={[
                    'model = "gpt-5.2"',
                    'model_reasoning_effort = "xhigh"',
                    "",
                    "# Added for 0-0.pro (only 6 lines)",
                    'model_provider = "0-0"',
                    "",
                    "[model_providers.0-0]",
                    'name = "0-0"',
                    'base_url = "https://0-0.pro/v1"',
                    'wire_api = "responses"',
                    "requires_openai_auth = true",
                    "# End added block",
                    "",
                    '[projects.\"/Users/xxx/Downloads/GitHub/cerebr\"]',
                    'trust_level = "trusted"',
                    "",
                    '[projects.\"/Users/xxx\"]',
                    'trust_level = "untrusted"',
                    "",
                    '[projects.\"/Users/xxx/.codex/sessions/2026/01/24\"]',
                    'trust_level = "untrusted"',
                    "",
                    "[notice.model_migrations]",
                    '\"gpt-5.2\" = \"gpt-5.2-codex\"'
                  ].join("\n")}
                />

                <p className="mt-4">
                  If you already have a config, you only need to add the following 6 lines (plus an optional blank line)
                  near the top:
                </p>
                <CodeBlock
                  lang="toml"
                  code={[
                    'model_provider = "0-0"',
                    "",
                    "[model_providers.0-0]",
                    'name = "0-0"',
                    'base_url = "https://0-0.pro/v1"',
                    'wire_api = "responses"',
                    "requires_openai_auth = true"
                  ].join("\n")}
                />

                <div className="mt-4 rounded-xl border border-border bg-background/35 p-4 text-sm text-muted-foreground">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="secondary">Note</Badge>
                    <span>
                      Keep your existing{" "}
                      <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">model</code> and{" "}
                      <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">
                        model_reasoning_effort
                      </code>{" "}
                      unchanged.
                    </span>
                  </div>
                </div>
              </>
            )
          },
          {
            id: "auth",
            title: "3) Update ~/.codex/auth.json",
            content: (
              <>
                <p>
                  Set your Uni API key as{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">OPENAI_API_KEY</code>:
                </p>
                <CodeBlock
                  lang="json"
                  code={JSON.stringify({ OPENAI_API_KEY: "your-api-key" }, null, 2)}
                />
              </>
            )
          },
          {
            id: "restart",
            title: "4) Restart VS Code",
            content: (
              <p>
                Restart VS Code (or reload the window) so Codex picks up the updated configuration.
              </p>
            )
          },
          {
            id: "history",
            title: "Why did my past chats disappear?",
            content: (
              <>
                <p>
                  Codex conversations are associated with the{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">model_provider</code>. All
                  session data is stored locally, but switching providers can make older sessions “invisible”.
                </p>
                <p>
                  To show older sessions under the new provider, find the JSONL files in{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">~/.codex/sessions</code>{" "}
                  and update the{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">model_provider</code>{" "}
                  in the first line:
                </p>
                <CodeBlock
                  lang="json"
                  code={'{ "model_provider": "openai", "...": "..." }  →  { "model_provider": "0-0", "...": "..." }'}
                />
                <p>Restart VS Code after the change to re-load the sessions.</p>
              </>
            )
          }
        ]
      },
      "zh-CN": {
        title: "Codex 配置",
        description: "让 Codex 使用 0-0.pro 作为模型提供商（wire_api = responses）。",
        sections: [
          {
            id: "install",
            title: "1）安装 Codex",
            content: (
              <p>
                在你的编辑器（VS Code）中安装 Codex。安装后，Codex 会从{" "}
                <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">~/.codex</code>{" "}
                读取配置。
              </p>
            )
          },
          {
            id: "config",
            title: "2）修改 ~/.codex/config.toml",
            content: (
              <>
                <p>
                  打开{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">
                    ~/.codex/config.toml
                  </code>
                  。下面给一个可以直接复制的完整示例；你只需要把{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">[projects.*]</code>{" "}
                  下的路径替换成自己的即可。
                </p>
                <CodeBlock
                  lang="toml"
                  code={[
                    'model = "gpt-5.2"',
                    'model_reasoning_effort = "xhigh"',
                    "",
                    "# 新增：0-0.pro 提供商（仅 6 行）",
                    'model_provider = "0-0"',
                    "",
                    "[model_providers.0-0]",
                    'name = "0-0"',
                    'base_url = "https://0-0.pro/v1"',
                    'wire_api = "responses"',
                    "requires_openai_auth = true",
                    "# 新增结束",
                    "",
                    '[projects.\"/Users/xxx/Downloads/GitHub/cerebr\"]',
                    'trust_level = "trusted"',
                    "",
                    '[projects.\"/Users/xxx\"]',
                    'trust_level = "untrusted"',
                    "",
                    '[projects.\"/Users/xxx/.codex/sessions/2026/01/24\"]',
                    'trust_level = "untrusted"',
                    "",
                    "[notice.model_migrations]",
                    '\"gpt-5.2\" = \"gpt-5.2-codex\"'
                  ].join("\n")}
                />

                <p className="mt-4">
                  如果你已经有自己的配置，只需要在文件顶部附近新增下面 6 行（可以包含 1 行空行）即可：
                </p>
                <CodeBlock
                  lang="toml"
                  code={[
                    'model_provider = "0-0"',
                    "",
                    "[model_providers.0-0]",
                    'name = "0-0"',
                    'base_url = "https://0-0.pro/v1"',
                    'wire_api = "responses"',
                    "requires_openai_auth = true"
                  ].join("\n")}
                />

                <div className="mt-4 rounded-xl border border-border bg-background/35 p-4 text-sm text-muted-foreground">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="secondary">提示</Badge>
                    <span>
                      原来的{" "}
                      <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">model</code>{" "}
                      与{" "}
                      <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">
                        model_reasoning_effort
                      </code>{" "}
                      保持不变即可。
                    </span>
                  </div>
                </div>
              </>
            )
          },
          {
            id: "auth",
            title: "3）修改 ~/.codex/auth.json",
            content: (
              <>
                <p>
                  将你的 Uni API 密钥写入{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">OPENAI_API_KEY</code>：
                </p>
                <CodeBlock lang="json" code={JSON.stringify({ OPENAI_API_KEY: "your-api-key" }, null, 2)} />
              </>
            )
          },
          {
            id: "restart",
            title: "4）重启 VS Code",
            content: <p>重启 VS Code（或 Reload Window），使 Codex 读取最新配置。</p>
          },
          {
            id: "history",
            title: "为什么过去的对话丢失了？",
            content: (
              <>
                <p>
                  Codex 的对话会跟随{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">model_provider</code>。
                  数据都在本地，但切换提供商会导致旧会话暂时“不显示”。
                </p>
                <p>
                  如果希望旧会话在新提供商下显示：在{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">~/.codex/sessions</code>{" "}
                  目录下找到对应的{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">.jsonl</code>{" "}
                  文件，把第一行里的{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">model_provider</code>{" "}
                  从{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">openai</code>{" "}
                  改为{" "}
                  <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground">0-0</code>：
                </p>
                <CodeBlock
                  lang="json"
                  code={'{ "model_provider": "openai", "...": "..." }  →  { "model_provider": "0-0", "...": "..." }'}
                />
                <p>修改后重启 VS Code，原来的对话就会重新出现。</p>
              </>
            )
          }
        ]
      }
    }
  }
] as const;

function resolvePageDefinitionById(pageId: string) {
  return DOCS_PAGES.find((page) => page.id === pageId) ?? null;
}

function resolvePageDefinitionBySlug(slug: readonly string[]) {
  const key = slug.join("/");
  return DOCS_PAGES.find((page) => page.slug.join("/") === key) ?? null;
}

export function getDocsNav(locale: Locale): readonly DocsNavGroup[] {
  return DOCS_GROUPS.map((group) => {
    const items: DocsNavItem[] = group.pageIds.flatMap((pageId) => {
      const def = resolvePageDefinitionById(pageId);
      if (!def) return [];
      const content = def.content[locale];
      return [
        {
          id: def.id,
          href: docsHref(locale, def.slug),
          title: content.title,
          slug: def.slug
        }
      ];
    });

    return {
      id: group.id,
      title: group.title[locale],
      items
    };
  });
}

export function getDocsPage(locale: Locale, slug: readonly string[]): DocsPage | null {
  const def = resolvePageDefinitionBySlug(slug);
  if (!def) return null;
  const content = def.content[locale];
  return {
    id: def.id,
    slug: def.slug,
    title: content.title,
    description: content.description,
    sections: content.sections
  };
}

const DOCS_FLAT_ORDER: readonly string[] = DOCS_GROUPS.flatMap((group) => group.pageIds);

export function getDocsPrevNext(locale: Locale, pageId: string) {
  const idx = DOCS_FLAT_ORDER.indexOf(pageId);
  if (idx < 0) return { prev: null, next: null } as const;
  const prevId = DOCS_FLAT_ORDER[idx - 1] ?? null;
  const nextId = DOCS_FLAT_ORDER[idx + 1] ?? null;

  const prevDef = prevId ? resolvePageDefinitionById(prevId) : null;
  const nextDef = nextId ? resolvePageDefinitionById(nextId) : null;

  const prev =
    prevDef
      ? {
          id: prevDef.id,
          href: docsHref(locale, prevDef.slug),
          title: prevDef.content[locale].title,
          slug: prevDef.slug
        }
      : null;
  const next =
    nextDef
      ? {
          id: nextDef.id,
          href: docsHref(locale, nextDef.slug),
          title: nextDef.content[locale].title,
          slug: nextDef.slug
        }
      : null;

  return { prev, next } as const;
}

export function getDocsAlternates(slug: readonly string[]) {
  return {
    en: docsHref("en", slug),
    "zh-CN": docsHref("zh-CN", slug)
  } as const;
}
