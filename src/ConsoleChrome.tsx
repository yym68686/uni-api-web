import {
  BookOpen,
  ChevronRight,
  ExternalLink,
  Gauge,
  LayoutDashboard,
  Menu,
  Moon,
  ScanLine,
  Server,
  SlidersHorizontal,
  Sun,
  Wallet,
  Bot,
  Search,
} from "lucide-react";
import type { View } from "./preferences";
import type { Theme } from "./theme";
import { Brand } from "./ui";

const pages = [
  { view: "overview", label: "总览", icon: Gauge },
  { view: "channels", label: "渠道观测", icon: LayoutDashboard },
  { view: "requests", label: "日志", icon: Search },
  { view: "sub2api", label: "渠道管理", icon: ScanLine, account: true },
  { view: "prices", label: "价格设置", icon: SlidersHorizontal },
  { view: "balances", label: "余额管理", icon: Wallet },
  { view: "sources", label: "设置", icon: Server, account: true },
  { view: "automations", label: "自动化", icon: Bot, account: true },
] as const;

export function ConsoleNavigation({
  view,
  account,
  lowCount = 0,
  onSelect,
  onGuide,
}: {
  view: View;
  account: boolean;
  lowCount?: number;
  onSelect?: (view: View) => void;
  onGuide?: () => void;
}) {
  return (
    <>
      <Brand />
      <div className="workspace-label">WORKSPACE</div>
      <nav aria-label="主导航">
        {pages
          .filter((page) => !("account" in page) || account)
          .map((page) => (
            <button
              key={page.view}
              className={view === page.view ? "active" : ""}
              aria-current={view === page.view ? "page" : undefined}
              disabled={!onSelect}
              onClick={() => onSelect?.(page.view)}
            >
              <page.icon size={18} />
              {page.label}
              {page.view === "balances" && lowCount > 0 && (
                <span className="nav-count">{lowCount}</span>
              )}
            </button>
          ))}
      </nav>
      <div className="sidebar-bottom">
        {onGuide && <button className="button ghost" onClick={onGuide}><BookOpen size={17} /> 指标说明</button>}
        <a
          href="https://github.com/yym68686/uni-api-web"
          target="_blank"
          rel="noreferrer"
        >
          <BookOpen size={18} />
          开源项目
          <ExternalLink size={14} />
        </a>
        <div className="sidebar-version">
          <span className="tiny-dot" /> uni-api console <small>2.0</small>
        </div>
      </div>
    </>
  );
}

export function ConsoleHeader({
  view,
  theme,
  onMenu,
  onTheme,
  onConnection,
  onDisconnect,
  identity,
  account,
}: {
  view: View;
  theme: Theme;
  onMenu?: () => void;
  onTheme?: () => void;
  onConnection?: () => void;
  onDisconnect?: () => void;
  identity?: string;
  account?: boolean;
}) {
  return (
    <header className="topbar">
      <div className="breadcrumb">
        <button
          className="icon-button menu-toggle"
          disabled={!onMenu}
          onClick={onMenu}
          aria-label="打开菜单"
        >
          <Menu size={20} />
        </button>
        <span>工作空间</span>
        <ChevronRight size={13} />
        <strong>{pages.find((page) => page.view === view)?.label}</strong>
      </div>
      <div className="topbar-actions">
        <button
          className="icon-button"
          disabled={!onTheme}
          onClick={onTheme}
          aria-label={theme === "dark" ? "切换浅色模式" : "切换深色模式"}
        >
          {theme === "dark" ? <Sun size={18} /> : <Moon size={18} />}
        </button>
        {onConnection && <details className="account-menu">
          <summary aria-label={account ? `当前账号 ${identity}` : "管理服务连接"}>
            <span className="avatar" aria-hidden="true">{identity?.trim().slice(0, 1).toUpperCase() || "↔"}</span>
            <span className="account-name">{identity || "直连"}</span>
          </summary>
          <div className="account-menu-panel">
            <strong>{identity || "直连服务"}</strong>
            <button className="button ghost" onClick={event => {
              event.currentTarget.closest("details")?.removeAttribute("open");
              onConnection();
            }}>{account ? "管理 uni-api 来源" : "切换服务连接"}</button>
            <button className="button ghost" onClick={onDisconnect}>{account ? "退出当前账号" : "断开当前连接"}</button>
          </div>
        </details>}
      </div>
    </header>
  );
}
