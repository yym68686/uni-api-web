import { useState } from "react";
import { ConsoleHeader, ConsoleNavigation } from "./ConsoleChrome";
import { loadView } from "./preferences";
import type { View } from "./preferences";
import { loadConnection } from "./session";
import { useTheme } from "./theme";

const panelTitles: Record<Exclude<View, "overview">, string> = {
  channels: "渠道表现",
  balances: "渠道余额",
  sub2api: "站点账号",
  prices: "模型价格",
  sources: "uni-api 来源",
  automations: "自动化",
};

function SkeletonPanel({ title }: { title: string }) {
  return (
    <section className="data-panel">
      <div className="data-heading">
        <div className="data-title">
          <h2>{title}</h2>
        </div>
        <div className="skeleton startup-search" />
      </div>
      <div className="table-skeleton">
        <div className="skeleton skeleton-header" />
        {Array.from({ length: 6 }, (_, i) => (
          <div className="skeleton skeleton-row" key={i} />
        ))}
      </div>
    </section>
  );
}

export function StartupScreen({ onRetry }: { onRetry?: () => void }) {
  const [theme] = useTheme();
  const [{ view, account }] = useState(() => {
    const connection = loadConnection();
    return {
      view: loadView(connection?.base || location.origin, !connection),
      account: !connection,
    };
  });
  const showMetrics =
    view === "overview" || view === "channels" || view === "balances";
  return (
    <div className="app-shell startup-screen" aria-busy={!onRetry}>
      <aside className="sidebar" aria-hidden="true" inert>
        <ConsoleNavigation view={view} account={account} />
      </aside>
      <div className="main-shell">
        <div aria-hidden="true" inert>
          <ConsoleHeader view={view} theme={theme} />
        </div>
        <main className="workspace">
          {onRetry ? (
            <div className="data-panel table-error" role="alert">
              <h2>账户服务暂不可用。</h2>
              <button className="button" onClick={onRetry}>
                重试
              </button>
            </div>
          ) : (
            <>
              <span className="sr-only" role="status">
                正在恢复会话…
              </span>
              <div aria-hidden="true" className="startup-content">
                {showMetrics && (
                  <div
                    className={
                      view === "overview" ? "overview-grid" : "metric-grid"
                    }
                  >
                    {Array.from(
                      { length: view === "overview" ? 8 : 4 },
                      (_, i) => (
                        <div className="metric-card" key={i}>
                          <div className="skeleton startup-label" />
                          <div className="skeleton startup-value" />
                          <div className="skeleton startup-note" />
                        </div>
                      ),
                    )}
                  </div>
                )}
                {view === "overview" ? null : (
                  <SkeletonPanel title={panelTitles[view]} />
                )}
                {view === "sub2api" && <SkeletonPanel title="模型检测" />}
              </div>
            </>
          )}
        </main>
      </div>
    </div>
  );
}
