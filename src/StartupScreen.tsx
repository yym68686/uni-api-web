import { useState } from "react";
import { ConsoleHeader, ConsoleNavigation } from "./ConsoleChrome";
import { loadView } from "./preferences";
import { PageLoading } from "./PageLoading";
import { loadConnection } from "./session";
import { useTheme } from "./theme";
import { readWorkspace } from "./workspaceLocation";

export function StartupScreen({ onRetry }: { onRetry?: () => void }) {
  const [theme] = useTheme();
  const [{ view, account }] = useState(() => {
    const connection = loadConnection();
    return {
      view: readWorkspace(location.hash, !connection).view || loadView(connection?.base || location.origin, !connection),
      account: !connection,
    };
  });
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
              <p>无法验证当前登录会话。渠道和历史数据尚未开始读取，已保存的配置不会被更改。</p>
              <button className="button" onClick={onRetry}>
                重试
              </button>
            </div>
          ) : <PageLoading view={view} label="正在恢复会话" />}
        </main>
      </div>
    </div>
  );
}
