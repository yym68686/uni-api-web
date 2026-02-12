"use client";

import * as React from "react";
import { Copy } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/i18n/i18n-provider";
import { cn } from "@/lib/utils";

interface CodeBlockProps {
  code: string;
  lang?: string;
  className?: string;
}

interface CodeToken {
  text: string;
  className?: string;
}

function pushToken(tokens: CodeToken[], token: CodeToken) {
  if (token.text.length === 0) return;
  const last = tokens[tokens.length - 1];
  if (last && last.className === token.className) {
    last.text += token.text;
    return;
  }
  tokens.push(token);
}

function tokenizeByRegex(
  input: string,
  re: RegExp,
  classify: (match: string) => CodeToken
): CodeToken[] {
  const tokens: CodeToken[] = [];
  let idx = 0;
  for (const match of input.matchAll(re)) {
    const value = match[0];
    const start = match.index ?? 0;
    if (start > idx) pushToken(tokens, { text: input.slice(idx, start) });
    pushToken(tokens, classify(value));
    idx = start + value.length;
  }
  if (idx < input.length) pushToken(tokens, { text: input.slice(idx) });
  return tokens;
}

function tokenizeJson(line: string): CodeToken[] {
  const stringRe = /"(?:\\.|[^"\\])*"/g;
  const tokens: CodeToken[] = [];
  let idx = 0;
  for (const match of line.matchAll(stringRe)) {
    const value = match[0];
    const start = match.index ?? 0;
    if (start > idx) {
      const chunk = line.slice(idx, start);
      for (const t of tokenizeByRegex(
        chunk,
        /(\btrue\b|\bfalse\b|\bnull\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|[{}\[\]:,])/g,
        (m) => {
          if (m === "true" || m === "false" || m === "null") return { text: m, className: "text-primary" };
          if (/^-?\d/.test(m)) return { text: m, className: "text-warning" };
          return { text: m, className: "text-muted-foreground/80" };
        }
      )) {
        pushToken(tokens, t);
      }
    }

    const rest = line.slice(start + value.length);
    const isKey = /^\s*:/.test(rest);
    pushToken(tokens, { text: value, className: isKey ? "text-primary" : "text-success" });
    idx = start + value.length;
  }

  if (idx < line.length) {
    const tail = line.slice(idx);
    for (const t of tokenizeByRegex(
      tail,
      /(\btrue\b|\bfalse\b|\bnull\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|[{}\[\]:,])/g,
      (m) => {
        if (m === "true" || m === "false" || m === "null") return { text: m, className: "text-primary" };
        if (/^-?\d/.test(m)) return { text: m, className: "text-warning" };
        return { text: m, className: "text-muted-foreground/80" };
      }
    )) {
      pushToken(tokens, t);
    }
  }

  return tokens;
}

function splitTomlComment(line: string): { code: string; comment: string | null } {
  let inSingle = false;
  let inDouble = false;
  for (let i = 0; i < line.length; i += 1) {
    const ch = line[i];
    if (ch === "'" && !inDouble) inSingle = !inSingle;
    if (ch === "\"" && !inSingle) inDouble = !inDouble;
    if (ch === "#" && !inSingle && !inDouble) {
      return { code: line.slice(0, i), comment: line.slice(i) };
    }
  }
  return { code: line, comment: null };
}

function tokenizeToml(line: string): CodeToken[] {
  const trimmed = line.trimStart();
  if (trimmed.startsWith("#")) {
    return [{ text: line, className: "text-muted-foreground/60 italic" }];
  }

  const { code, comment } = splitTomlComment(line);
  const codeTokens: CodeToken[] = [];

  const sectionMatch = /^\s*\[[^\]]+\]\s*$/.exec(code);
  if (sectionMatch) {
    const raw = sectionMatch[0];
    const openIdx = raw.indexOf("[");
    const closeIdx = raw.lastIndexOf("]");
    pushToken(codeTokens, { text: raw.slice(0, openIdx + 1), className: "text-muted-foreground/80" });
    pushToken(codeTokens, { text: raw.slice(openIdx + 1, closeIdx), className: "text-primary" });
    pushToken(codeTokens, { text: raw.slice(closeIdx), className: "text-muted-foreground/80" });
  } else {
    const eqIdx = code.indexOf("=");
    if (eqIdx >= 0) {
      const left = code.slice(0, eqIdx);
      const right = code.slice(eqIdx + 1);
      const leftMatch = /^(\s*)([A-Za-z0-9_.-]+)(\s*)$/.exec(left);
      if (leftMatch) {
        pushToken(codeTokens, { text: leftMatch[1] ?? "" });
        pushToken(codeTokens, { text: leftMatch[2] ?? "", className: "text-primary" });
        pushToken(codeTokens, { text: leftMatch[3] ?? "" });
      } else {
        pushToken(codeTokens, { text: left });
      }
      pushToken(codeTokens, { text: "=", className: "text-muted-foreground/80" });

      const valueTokens = tokenizeByRegex(
        right,
        /("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\btrue\b|\bfalse\b|-?\d+(?:\.\d+)?)/g,
        (m) => {
          if (m === "true" || m === "false") return { text: m, className: "text-primary" };
          if (m.startsWith("\"") || m.startsWith("'")) return { text: m, className: "text-success" };
          if (/^-?\d/.test(m)) return { text: m, className: "text-warning" };
          return { text: m };
        }
      );
      for (const t of valueTokens) pushToken(codeTokens, t);
    } else {
      pushToken(codeTokens, { text: code });
    }
  }

  if (comment) pushToken(codeTokens, { text: comment, className: "text-muted-foreground/60 italic" });
  return codeTokens;
}

function tokenizeBash(line: string): CodeToken[] {
  const trimmed = line.trimStart();
  if (trimmed.startsWith("#")) {
    return [{ text: line, className: "text-muted-foreground/60 italic" }];
  }

  const stringRe = /"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'/g;
  const tokens: CodeToken[] = [];
  let idx = 0;
  for (const match of line.matchAll(stringRe)) {
    const value = match[0];
    const start = match.index ?? 0;
    if (start > idx) {
      const chunk = line.slice(idx, start);
      for (const t of tokenizeByRegex(chunk, /(\$\{?[A-Za-z_][A-Za-z0-9_]*\}?|--?[A-Za-z0-9][A-Za-z0-9-]*|\\)/g, (m) => {
        if (m === "\\") return { text: m, className: "text-muted-foreground/80" };
        if (m.startsWith("$")) return { text: m, className: "text-primary" };
        if (m.startsWith("-")) return { text: m, className: "text-warning" };
        return { text: m };
      })) {
        pushToken(tokens, t);
      }
    }
    pushToken(tokens, { text: value, className: "text-success" });
    idx = start + value.length;
  }
  if (idx < line.length) {
    const tail = line.slice(idx);
    for (const t of tokenizeByRegex(tail, /(\$\{?[A-Za-z_][A-Za-z0-9_]*\}?|--?[A-Za-z0-9][A-Za-z0-9-]*|\\)/g, (m) => {
      if (m === "\\") return { text: m, className: "text-muted-foreground/80" };
      if (m.startsWith("$")) return { text: m, className: "text-primary" };
      if (m.startsWith("-")) return { text: m, className: "text-warning" };
      return { text: m };
    })) {
      pushToken(tokens, t);
    }
  }
  return tokens;
}

function tokenizeHttp(line: string): CodeToken[] {
  const reqLine = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)(\s+)(\S+)(\s+)(HTTP\/[0-9.]+)\s*$/.exec(line);
  if (reqLine) {
    return [
      { text: reqLine[1] ?? "", className: "text-primary" },
      { text: reqLine[2] ?? "" },
      { text: reqLine[3] ?? "", className: "text-success" },
      { text: reqLine[4] ?? "" },
      { text: reqLine[5] ?? "", className: "text-muted-foreground/80" }
    ];
  }

  const headerIdx = line.indexOf(":");
  if (headerIdx > 0) {
    const name = line.slice(0, headerIdx);
    const rest = line.slice(headerIdx + 1);
    const nameMatch = /^(\s*)([^:\s]+)(\s*)$/.exec(name);
    const tokens: CodeToken[] = [];
    if (nameMatch) {
      pushToken(tokens, { text: nameMatch[1] ?? "" });
      pushToken(tokens, { text: nameMatch[2] ?? "", className: "text-primary" });
      pushToken(tokens, { text: nameMatch[3] ?? "" });
    } else {
      pushToken(tokens, { text: name, className: "text-primary" });
    }
    pushToken(tokens, { text: ":", className: "text-muted-foreground/80" });
    pushToken(tokens, { text: rest });
    return tokens;
  }

  return [{ text: line }];
}

function tokenizeLine(lang: string | undefined, line: string): CodeToken[] {
  const normalized = (lang ?? "").trim().toLowerCase();
  if (normalized === "json") return tokenizeJson(line);
  if (normalized === "toml") return tokenizeToml(line);
  if (normalized === "bash" || normalized === "sh" || normalized === "shell") return tokenizeBash(line);
  if (normalized === "http") return tokenizeHttp(line);
  return [{ text: line }];
}

export function CodeBlock({ code, lang, className }: CodeBlockProps) {
  const { t } = useI18n();
  const [copied, setCopied] = React.useState(false);
  const normalizedCode = React.useMemo(() => code.replace(/\r\n/g, "\n"), [code]);
  const lines = React.useMemo(() => normalizedCode.split("\n"), [normalizedCode]);
  const highlighted = React.useMemo(() => lines.map((line) => tokenizeLine(lang, line)), [lang, lines]);
  const gutterChars = Math.max(String(lines.length).length, 2);
  const lineGridStyle = React.useMemo<React.CSSProperties>(
    () => ({ gridTemplateColumns: `${gutterChars}ch 1fr` }),
    [gutterChars]
  );

  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      toast.success(t("common.copied"));
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      toast.error(t("common.copyFailed"));
    }
  }

  return (
    <div
      className={cn(
        "group relative overflow-hidden rounded-xl border border-border bg-card/40 backdrop-blur",
        "shadow-[0_0_0_1px_oklch(var(--border)/0.55),0_10px_30px_oklch(0%_0_0/0.28)]",
        className
      )}
    >
      <div className="flex items-center justify-between gap-2 border-b border-border bg-background/30 px-3 py-2">
        <span className="truncate text-xs text-muted-foreground">
          <span className="font-mono">{lang ?? "code"}</span>
        </span>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-8 rounded-lg px-2"
          onClick={copy}
          aria-label={t("common.copy")}
        >
          <Copy suppressHydrationWarning className="h-4 w-4" />
          <span className="text-xs">{copied ? t("common.copied") : t("common.copy")}</span>
        </Button>
      </div>
      <pre className="scrollbar-hide overflow-x-auto p-4 text-xs leading-relaxed">
        <code className="block min-w-max font-mono text-foreground">
          {highlighted.map((tokens, idx) => (
            <span
              key={idx}
              className="grid min-w-max gap-4"
              style={lineGridStyle}
            >
              <span className="select-none text-right tabular-nums text-muted-foreground/60">
                {idx + 1}
              </span>
              <span className="whitespace-pre">
                {tokens.length === 0 ? "\u00A0" : null}
                {tokens.map((token, tokenIdx) => (
                  <span
                    key={tokenIdx}
                    className={token.className}
                  >
                    {token.text === "" ? "\u00A0" : token.text}
                  </span>
                ))}
              </span>
            </span>
          ))}
        </code>
      </pre>
    </div>
  );
}
