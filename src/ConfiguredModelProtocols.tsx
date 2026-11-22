import { useEffect, useRef, useState } from "react";
import { controlRequest } from "./api";
import { configuredChannelProtocols } from "./channelProtocol";

export function ConfiguredModelProtocols({ source, revision, models, saved, choices, suggested, onChange, disabled, onDetecting }: {
  source: string;
  revision: string;
  models: { upstream: string; provider: string; model: string; supported: string[] }[];
  saved: Record<string, string>;
  choices: Record<string, string>;
  suggested: Record<string, string>;
  onChange: (model: string, protocol: string) => void;
  disabled: boolean;
  onDetecting: (detecting: boolean) => void;
}) {
  const [testing, setTesting] = useState("");
  const [messages, setMessages] = useState<Record<string, string>>({});
  const [confirmed, setConfirmed] = useState<Record<string, string[]>>({});
  const active = useRef<AbortController | null>(null);
  useEffect(() => () => {
    active.current?.abort();
    onDetecting(false);
  }, [onDetecting]);
  async function detect({ upstream, provider, model, supported }: typeof models[number]) {
    if (active.current || disabled) return;
    const abort = new AbortController();
    active.current = abort;
    setTesting(upstream);
    onDetecting(true);
    setMessages(old => ({ ...old, [upstream]: "" }));
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
      setMessages(old => ({ ...old, [upstream]: match ? `已确认 ${available.length} 个可用端点；当前选择：${match.label}，保存后生效。` : "未确认可用端点，已保留当前选择。" }));
    } catch (error) {
      if (!abort.signal.aborted) setMessages(old => ({ ...old, [upstream]: error instanceof Error ? error.message : "端点检测失败" }));
    } finally {
      if (!abort.signal.aborted) {
        active.current = null;
        setTesting("");
        onDetecting(false);
      }
    }
  }
  return (
    <section className="import-protocols" aria-label="各模型上游端点">
      <div className="route-section-heading"><h3>各模型上游端点</h3></div>
      <p className="muted">列表显示可配置的端点；自动检测会逐一向上游发送测试请求，并标记实测可用的端点。System One 仅用于结构化请求。同一实际模型的别名共享端点。</p>
      {models.map(item => {
        const { upstream, supported } = item;
        const value = choices[upstream] ?? saved[upstream] ?? suggested[upstream] ?? "";
        return <div className="configured-protocol-item" key={upstream}>
          <label className="import-protocol-row">
            <span title={upstream}>{upstream}</span>
            <select aria-label={`${upstream} 上游端点`} disabled={disabled || !!testing}
              value={value}
              onChange={event => {
                onChange(upstream, event.target.value);
                setMessages(old => ({ ...old, [upstream]: "" }));
              }}>
              <option value="">沿用渠道默认端点</option>
              {configuredChannelProtocols.filter(({ protocol }) => supported.includes(protocol) || protocol === value).map(({ protocol, label, endpoint }) => <option key={protocol} value={protocol} disabled={!supported.includes(protocol)}>{label} · {endpoint}{!supported.includes(protocol) ? "（不可配置，请重选）" : confirmed[upstream]?.includes(protocol) ? "（实测可用）" : ""}</option>)}
            </select>
          </label>
          <button className="button small" type="button" disabled={disabled || !!testing || !supported.length}
            aria-label={`自动检测 ${upstream} 的上游端点`} onClick={() => void detect(item)}>
            {testing === upstream ? "检测中…" : "自动检测"}
          </button>
          {!supported.length ? <small className="muted">此模型没有可切换的已知端点。</small> : messages[upstream] ? <small className="muted" role="status">{messages[upstream]}</small> :
            suggested[upstream] && !Object.hasOwn(choices, upstream) && <small className="muted">已通过模型检测，保存后使用此端点。</small>}
        </div>;
      })}
    </section>
  );
}
