import { useMemo, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import {
  Bot, Gauge, ListTree, Menu, Moon, Network, Route, Search,
  Settings, Sun, Tag, Wallet, Waypoints, X,
} from "./icons";
import type { View } from "./preferences";
import type { Theme } from "./theme";
import type { Channel } from "./types";
import { channelName, rowId } from "./format";
import { LoadingIndicator } from "./LoadingIndicator";

const groups = [
  { name: "观测", pages: [
    { view: "overview", label: "总览", icon: Gauge },
    { view: "channels", label: "渠道观测", icon: Network },
    { view: "requests", label: "请求日志", icon: ListTree },
  ] },
  { name: "管理", pages: [
    { view: "sub2api", label: "渠道管理", icon: Route, account: true },
    { view: "balances", label: "余额管理", icon: Wallet },
    { view: "prices", label: "模型价格", icon: Tag },
    { view: "automations", label: "自动化", icon: Bot, account: true },
  ] },
] as const;
const settings = { view: "sources", label: "设置", icon: Settings, account: true } as const;
const pages = [...groups.flatMap(group => [...group.pages]), settings];
type ConsolePage = (typeof pages)[number];
const allowed = (page: ConsolePage, account: boolean) => account || !("account" in page);

export function ConsoleBrand() {
  return <div className="observatory-brand"><span><Waypoints size={16} aria-hidden="true" /></span>uni-api</div>;
}

export function ConsoleNavigation({ view, account, onSelect }: {
  view: View;
  account: boolean;
  onSelect?: (view: View) => void;
}) {
  const current = view === "routing" ? "sub2api" : view;
  const link = (page: ConsolePage) => (
    <button key={page.view} type="button" aria-current={current === page.view ? "page" : undefined}
      disabled={!onSelect} onClick={() => onSelect?.(page.view)}>
      <page.icon size={16} aria-hidden="true" />{page.label}
    </button>
  );
  return (
    <>
      <div className="observatory-rail-brand"><ConsoleBrand /></div>
      <nav className="observatory-nav" aria-label="主导航">
        {groups.map(group => <div key={group.name} className="observatory-nav-group">
          <h2 className="sr-only">{group.name}</h2>
          {group.pages.filter(page => allowed(page, account)).map(link)}
        </div>)}
        {account && <div className="observatory-nav-end">{link(settings)}</div>}
      </nav>
    </>
  );
}

export function ConsoleHeader({ theme, onMenu, onTheme, onConnection, onDisconnect, identity, account, onLocate, onGuide }: {
  view: View;
  theme: Theme;
  onMenu?: () => void;
  onTheme?: () => void;
  onConnection?: () => void;
  onDisconnect?: () => void;
  identity?: string;
  account?: boolean;
  onLocate?: () => void;
  onGuide?: () => void;
}) {
  return (
    <header className="topbar observatory-topbar">
      <div className="observatory-context">
        <button type="button" className="icon-button menu-toggle" disabled={!onMenu} onClick={onMenu} aria-label="打开菜单"><Menu size={18} /></button>
        <div className="observatory-mobile-brand"><ConsoleBrand /></div>
        {!account && <span className="observatory-environment">直连</span>}
      </div>
      <div className="topbar-actions">
        {onLocate && <button type="button" className="locate-trigger" onClick={onLocate} aria-keyshortcuts="Meta+K Control+K">
          <Search size={15} aria-hidden="true" />定位<kbd>{/Mac|iPhone|iPad/i.test(navigator.platform) ? "⌘K" : "Ctrl K"}</kbd>
        </button>}
        {onConnection && <details className="account-menu">
          <summary aria-label={account ? `当前账号 ${identity}` : "管理服务连接"}>
            <span className="avatar" aria-hidden="true">{identity?.trim().slice(0, 1).toUpperCase() || "↔"}</span>
          </summary>
          <div className="account-menu-panel">
            <strong>{identity || "直连服务"}</strong>
            <button type="button" className="button ghost" onClick={event => {
              event.currentTarget.closest("details")?.removeAttribute("open");
              onConnection();
            }}>{account ? "设置来源" : "切换连接"}</button>
            <button type="button" className="button ghost" onClick={onTheme}>
              {theme === "dark" ? <Sun size={15} /> : <Moon size={15} />}{theme === "dark" ? "浅色模式" : "深色模式"}
            </button>
            {onGuide && <button type="button" className="button ghost" onClick={event => {
              event.currentTarget.closest("details")?.removeAttribute("open");
              onGuide();
            }}>指标定义</button>}
            <button type="button" className="button ghost" onClick={onDisconnect}>{account ? "退出登录" : "断开连接"}</button>
          </div>
        </details>}
      </div>
    </header>
  );
}

const requestIDPattern = /^[A-Za-z0-9][A-Za-z0-9._:/+-]{1,511}$/;
export function ConsoleLocator({ open, onOpenChange, account, channels, loading, failed, onPage, onChannel, onRequest }: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  account: boolean;
  channels: Channel[];
  loading: boolean;
  failed: boolean;
  onPage: (view: View) => void;
  onChannel: (row: Channel) => void;
  onRequest: (id: string) => void;
}) {
  const [text, setText] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);
  const resultsRef = useRef<HTMLDivElement>(null);
  const value = text.trim();
  const needle = value.toLowerCase();
  const pageMatches = pages.filter(page => allowed(page, account) && page.label.toLowerCase().includes(needle));
  const channelMatches = useMemo(() => {
    if (!needle) return [];
    const matches: Channel[] = [], seen = new Set<string>();
    for (const row of channels) {
      if (!`${channelName(row)} ${row.provider} ${row.model} ${row.source_name || ""}`.toLowerCase().includes(needle)) continue;
      const id = rowId(row);
      if (seen.has(id)) continue;
      seen.add(id);
      matches.push(row);
      if (matches.length === 6) break;
    }
    return matches;
  }, [channels, needle]);
  const requestMatch = requestIDPattern.test(value);
  function closeThen(action: () => void) {
    setText("");
    onOpenChange(false);
    action();
  }
  return <Dialog.Root open={open} onOpenChange={next => { if (!next) setText(""); onOpenChange(next); }}>
    <Dialog.Portal>
      <Dialog.Overlay className="dialog-overlay" />
      <Dialog.Content className="connection-dialog observatory-locator"
        onOpenAutoFocus={event => {
          event.preventDefault();
          searchRef.current?.focus();
        }}
        onCloseAutoFocus={event => event.preventDefault()}>
        <div className="locator-heading"><Dialog.Title>定位</Dialog.Title>
          <Dialog.Close className="icon-button" aria-label="关闭定位"><X size={18} /></Dialog.Close>
        </div>
        <Dialog.Description className="sr-only">搜索页面、渠道、模型或请求 ID</Dialog.Description>
        <label className="locator-search">
          <Search size={17} aria-hidden="true" />
          <input ref={searchRef} type="search" aria-label="定位页面、渠道或请求 ID" placeholder="页面、渠道或请求 ID" value={text}
            onChange={event => setText(event.target.value)}
            onKeyDown={event => {
              if (event.nativeEvent.isComposing) return;
              const first = resultsRef.current?.querySelector<HTMLButtonElement>("button");
              if (event.key === "ArrowDown" || event.key === "Enter") {
                event.preventDefault();
                if (event.key === "Enter") first?.click();
                else first?.focus();
              }
            }} />
        </label>
        <div ref={resultsRef} className="locator-results">
          {pageMatches.map(page => <button key={page.view} type="button" onClick={() => closeThen(() => onPage(page.view))}>
            <page.icon size={15} aria-hidden="true" /><span>{page.label}</span>
          </button>)}
          {channelMatches.map(row => <button key={rowId(row)} type="button" onClick={() => closeThen(() => onChannel(row))}>
            <Network size={15} aria-hidden="true" /><span>{channelName(row)}<small>{[row.source_name, row.model].filter(Boolean).join(" · ")}</small></span>
          </button>)}
          {requestMatch && <button type="button" onClick={() => closeThen(() => onRequest(value))}>
            <ListTree size={15} aria-hidden="true" /><span>追踪请求<small>{value}</small></span>
          </button>}
          {needle && (loading || failed || (!pageMatches.length && !channelMatches.length && !requestMatch)) &&
            (loading ? <LoadingIndicator size="compact" label="读取渠道" /> : <p role="status">{failed ? "暂时无法读取渠道" : "没有匹配结果"}</p>)}
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
