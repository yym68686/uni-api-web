import { useEffect, useRef, useState } from "react";
import { controlRequest } from "./api";
import { channelProtocols } from "./channelProtocol";

export function ConfiguredModelProtocols({ source, revision, models, saved, choices, suggested, onChange, disabled, onDetecting }: {
  source: string;
  revision: string;
  models: { upstream: string; provider: string; model: string }[];
  saved: Record<string, string>;
  choices: Record<string, string>;
  suggested: Record<string, string>;
  onChange: (model: string, protocol: string) => void;
  disabled: boolean;
  onDetecting: (detecting: boolean) => void;
}) {
  const [testing, setTesting] = useState("");
  const [messages, setMessages] = useState<Record<string, string>>({});
  const active = useRef<AbortController | null>(null);
  useEffect(() => () => {
    active.current?.abort();
    onDetecting(false);
  }, [onDetecting]);
  async function detect({ upstream, provider, model }: typeof models[number]) {
    if (active.current || disabled) return;
    const abort = new AbortController();
    active.current = abort;
    setTesting(upstream);
    onDetecting(true);
    setMessages(old => ({ ...old, [upstream]: "" }));
    try {
      const { protocol } = await controlRequest<{ protocol: string }>("/v1/channel-management/detect-protocol", {
        method: "POST",
        signal: AbortSignal.any([abort.signal, AbortSignal.timeout(48_000)]),
        body: JSON.stringify({ source_id: source, revision, provider, model, upstream_model: upstream }),
      });
      if (abort.signal.aborted) return;
      const match = channelProtocols.find(option => option.protocol === protocol);
      if (match) onChange(upstream, match.protocol);
      setMessages(old => ({ ...old, [upstream]: match ? `已检测到 ${match.label}，保存后生效` : "未确认可用端点，已保留当前选择。" }));
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
      <p className="muted">可手动选协议，或自动检测后再保存。检测会向上游发送测试请求；端点设置按实际模型共享，同一模型的别名使用相同端点。</p>
      {models.map(item => {
        const { upstream } = item;
        return <div className="configured-protocol-item" key={upstream}>
          <label className="import-protocol-row">
            <span title={upstream}>{upstream}</span>
            <select aria-label={`${upstream} 上游端点`} disabled={disabled || !!testing}
              value={choices[upstream] ?? saved[upstream] ?? suggested[upstream] ?? ""}
              onChange={event => {
                onChange(upstream, event.target.value);
                setMessages(old => ({ ...old, [upstream]: "" }));
              }}>
              <option value="">沿用渠道默认端点</option>
              {channelProtocols.map(({ protocol, label, endpoint }) => <option key={protocol} value={protocol}>{label} · {endpoint}</option>)}
            </select>
          </label>
          <button className="button small" type="button" disabled={disabled || !!testing}
            aria-label={`自动检测 ${upstream} 的上游端点`} onClick={() => void detect(item)}>
            {testing === upstream ? "检测中…" : "自动检测"}
          </button>
          {messages[upstream] ? <small className="muted" role="status">{messages[upstream]}</small> :
            suggested[upstream] && !Object.hasOwn(choices, upstream) && <small className="muted">已通过模型检测，保存后使用此端点。</small>}
        </div>;
      })}
    </section>
  );
}
