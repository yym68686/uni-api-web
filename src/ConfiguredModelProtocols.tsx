import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { controlRequest } from "./api";
import { configuredChannelProtocols } from "./channelProtocol";
import { ScanLine } from "./icons";
import { Spinner } from "./ui";

export function ConfiguredModelProtocols({ source, revision, scope, models, saved, choices, suggested, onChange, disabled, onDetecting, render }: {
  source: string;
  revision: string;
  scope?: string;
  models: { upstream: string; provider: string; model: string; supported: string[] }[];
  saved: Record<string, string>;
  choices: Record<string, string>;
  suggested: Record<string, string>;
  onChange: (model: string, protocol: string) => void;
  disabled: boolean;
  onDetecting: (detecting: boolean) => void;
  render?: (controls: (upstream: string) => ReactNode) => ReactNode;
}) {
  const [testing, setTesting] = useState("");
  const [messages, setMessages] = useState<Record<string, { text: string; failed: boolean }>>({});
  const [confirmed, setConfirmed] = useState<Record<string, string[]>>({});
  const active = useRef<AbortController | null>(null);
  useEffect(() => {
    setTesting("");
    setMessages({});
    setConfirmed({});
    return () => {
      active.current?.abort();
      active.current = null;
      onDetecting(false);
    };
  }, [onDetecting, scope]);
  async function detect({ upstream, provider, model, supported }: typeof models[number]) {
    if (active.current || disabled) return;
    const abort = new AbortController();
    active.current = abort;
    setTesting(upstream);
    onDetecting(true);
    const clearMessage = () => setMessages(({ [upstream]: _, ...rest }) => rest);
    clearMessage();
    try {
      const { protocol, confirmed_protocols } = await controlRequest<{ protocol: string; confirmed_protocols?: string[] }>("/v1/channel-management/detect-protocol", {
        method: "POST",
        signal: AbortSignal.any([abort.signal, AbortSignal.timeout(48_000)]),
        body: JSON.stringify({ source_id: source, revision, provider, model, upstream_model: upstream }),
      });
      if (abort.signal.aborted) return;
      const available = (confirmed_protocols ?? [protocol]).filter(p => supported.includes(p));
      const current = choices[upstream] ?? saved[upstream] ?? suggested[upstream];
      const next = current && available.includes(current) ? current : available[0];
      const match = configuredChannelProtocols.find(option => option.protocol === next);
      setConfirmed(old => ({ ...old, [upstream]: available }));
      if (match && next !== current) onChange(upstream, match.protocol);
      setMessages(old => ({ ...old, [upstream]: { text: match ? `已确认 ${available.length} 个可用端点；当前选择：${match.label}，保存后生效。` : "未确认可用端点", failed: !match } }));
    } catch (error) {
      if (!abort.signal.aborted) setMessages(old => ({ ...old, [upstream]: { text: error instanceof Error ? error.message : "端点检测失败", failed: true } }));
    } finally {
      if (!abort.signal.aborted) {
        active.current = null;
        setTesting("");
        onDetecting(false);
      }
    }
  }
  function controls(item: typeof models[number], compact = false) {
        const { upstream, supported } = item;
        const value = choices[upstream] ?? saved[upstream] ?? suggested[upstream] ?? "";
        return <div className="configured-protocol-item" key={upstream}>
          <label className="import-protocol-row">
            {!compact && <span title={upstream}>{upstream}</span>}
            <select aria-label={`${upstream} 上游端点`} disabled={disabled || !!testing}
              value={value}
              onChange={event => {
                onChange(upstream, event.target.value);
                setMessages(({ [upstream]: _, ...rest }) => rest);
              }}>
              <option value="">{compact ? "渠道默认" : "沿用渠道默认端点"}</option>
              {configuredChannelProtocols.filter(({ protocol }) => supported.includes(protocol) || protocol === value).map(({ protocol, label, endpoint }) => <option key={protocol} value={protocol} disabled={!supported.includes(protocol)}>{label} · {endpoint}{!supported.includes(protocol) ? "（不可配置，请重选）" : confirmed[upstream]?.includes(protocol) ? "（实测可用）" : ""}</option>)}
            </select>
          </label>
          <button className={compact ? "settings-icon-button" : "button small"} type="button" disabled={disabled || !!testing || !supported.length}
            title={compact ? "自动检测端点" : undefined}
            aria-label={`自动检测 ${upstream} 的上游端点`} onClick={() => void detect(item)}>
            {compact ? (testing === upstream ? <Spinner small /> : <ScanLine size={16} />) : testing === upstream ? "检测中…" : "自动检测"}
          </button>
          {compact ? messages[upstream]?.failed && <small className="negative" role="alert">{messages[upstream].text}</small> :
            !supported.length ? <small className="muted">此模型没有可切换的已知端点。</small> : messages[upstream] ? <small className="muted" role="status">{messages[upstream].text}</small> :
            suggested[upstream] && !Object.hasOwn(choices, upstream) && <small className="muted">已通过模型检测，保存后使用此端点。</small>}
        </div>;
  }
  if (render) {
    const byModel = new Map(models.map(model => [model.upstream, model]));
    return render(upstream => {
      const item = byModel.get(upstream);
      return item ? controls(item, true) : null;
    });
  }
  return (
    <section className="import-protocols" aria-label="各模型上游端点">
      <div className="route-section-heading"><h3>各模型上游端点</h3></div>
      <p className="muted">列表显示可配置的端点；自动检测会逐一向上游发送测试请求，并标记实测可用的端点。System One 仅用于结构化请求。同一实际模型的别名共享端点。</p>
      {models.map(item => controls(item))}
    </section>
  );
}
