import { Plus, Trash2, ArrowUp, ArrowDown } from "lucide-react";

type Rule = { match: Record<string, unknown>; timeout: Record<string, number> };
type Policy = { default?: Record<string, number>; rules?: Rule[] };
const phases = [
  ["connect", "连接"],
  ["write", "写入"],
  ["pool", "连接池等待"],
  ["first_byte", "首字"],
  ["idle", "流式空闲"],
  ["total", "总时长"],
] as const;
const conditionText = (v: unknown) =>
  Array.isArray(v) ? v.join(", ") : typeof v === "string" ? v : "";
const conditionValue = (text: string) => {
  const parts = text
    .split(",")
    .map((v) => v.trim())
    .filter(Boolean);
  return parts.length > 1 ? parts : parts[0];
};
const endpoints = [
  "/v1/responses",
  "/v1/chat/completions",
  "/v1/messages",
  "/v1/responses/compact",
  "/v1/images/generations",
  "/v1/systemone",
];
export function TimeoutPolicyField({
  value,
  onChange,
}: {
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const policy = (
    value && typeof value === "object" && !Array.isArray(value) ? value : {}
  ) as Policy;
  const rules = Array.isArray(policy.rules) ? policy.rules : [];
  const update = (index: number, rule: Rule) =>
    onChange({
      ...policy,
      rules: rules.map((r, i) => (i === index ? rule : r)),
    });
  function timeoutInputs(
    values: Record<string, number>,
    label: string,
    save: (next: Record<string, number>) => void,
  ) {
    return phases.map(([key, name]) => (
      <label key={key}>
        {name}（秒）
        <input
          aria-label={`${label} ${name}超时（秒）`}
          type="number"
          min="0"
          step="any"
          placeholder="继承"
          value={values[key] ?? ""}
          onChange={(e) => {
            const next = { ...values };
            if (e.target.value === "") delete next[key];
            else next[key] = Number(e.target.value);
            save(next);
          }}
        />
      </label>
    ));
  }
  return (
    <div className="timeout-policy-editor">
      <p className="muted">
        按端点、流式状态或模型匹配。匹配条件最多的规则优先；条件数相同时使用靠前的规则。0
        表示关闭对应超时，留空继续继承。
      </p>
      <div className="timeout-phase-grid">
        {timeoutInputs(policy.default || {}, "默认", (next) =>
          onChange({ ...policy, default: next }),
        )}
      </div>
      {rules.map((rule, index) => (
        <section className="timeout-rule" key={index}>
          <div className="timeout-rule-heading">
            <strong>规则 {index + 1}</strong>
            <button
              type="button"
              className="icon-button"
              aria-label={`上移超时规则 ${index + 1}`}
              disabled={index === 0}
              onClick={() => {
                const next = [...rules];
                [next[index - 1], next[index]] = [next[index], next[index - 1]];
                onChange({ ...policy, rules: next });
              }}
            >
              <ArrowUp size={15} />
            </button>
            <button
              type="button"
              className="icon-button"
              aria-label={`下移超时规则 ${index + 1}`}
              disabled={index === rules.length - 1}
              onClick={() => {
                const next = [...rules];
                [next[index + 1], next[index]] = [next[index], next[index + 1]];
                onChange({ ...policy, rules: next });
              }}
            >
              <ArrowDown size={15} />
            </button>
            <button
              type="button"
              className="icon-button"
              aria-label={`删除超时规则 ${index + 1}`}
              onClick={() =>
                onChange({
                  ...policy,
                  rules: rules.filter((_, i) => i !== index),
                })
              }
            >
              <Trash2 size={15} />
            </button>
          </div>
          <div className="timeout-match-grid">
            <label>
              端点
              <input
                aria-label={`规则 ${index + 1} 端点`}
                list="timeout-endpoints"
                placeholder="全部端点"
                value={conditionText(rule.match?.endpoint)}
                onChange={(e) => {
                  const match = { ...rule.match };
                  if (e.target.value)
                    match.endpoint = conditionValue(e.target.value);
                  else delete match.endpoint;
                  update(index, { ...rule, match });
                }}
              />
            </label>
            <label>
              流式状态
              <select
                aria-label={`规则 ${index + 1} 流式状态`}
                value={
                  typeof rule.match?.stream === "boolean"
                    ? String(rule.match.stream)
                    : "all"
                }
                onChange={(e) => {
                  const match = { ...rule.match };
                  if (e.target.value === "all") delete match.stream;
                  else match.stream = e.target.value === "true";
                  update(index, { ...rule, match });
                }}
              >
                <option value="all">全部</option>
                <option value="true">流式</option>
                <option value="false">非流式</option>
              </select>
            </label>
            <label>
              模型
              <input
                aria-label={`规则 ${index + 1} 模型`}
                placeholder="全部模型；逗号分隔，支持 * 通配符"
                value={conditionText(rule.match?.model)}
                onChange={(e) => {
                  const match = { ...rule.match };
                  if (e.target.value)
                    match.model = conditionValue(e.target.value);
                  else delete match.model;
                  update(index, { ...rule, match });
                }}
              />
            </label>
          </div>
          {Object.keys(rule.match || {}).some(
            (k) => !["endpoint", "stream", "model"].includes(k),
          ) && (
            <p className="muted">
              本规则还包含其他匹配条件，可在下方 JSON 中查看和编辑。
            </p>
          )}
          <div className="timeout-phase-grid">
            {timeoutInputs(rule.timeout || {}, `规则 ${index + 1}`, (next) =>
              update(index, { ...rule, timeout: next }),
            )}
          </div>
        </section>
      ))}
      <datalist id="timeout-endpoints">
        {endpoints.map((e) => (
          <option key={e} value={e} />
        ))}
      </datalist>
      <button
        type="button"
        className="button small"
        onClick={() =>
          onChange({
            ...policy,
            rules: [
              ...rules,
              {
                match: { endpoint: "/v1/responses", stream: true },
                timeout: {},
              },
            ],
          })
        }
      >
        <Plus size={15} />
        添加超时规则
      </button>
    </div>
  );
}

export function HedgingField({
  value,
  onChange,
}: {
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const v = (value && typeof value === "object" ? value : {}) as Record<
    string,
    unknown
  >;
  return (
    <div className="hedging-editor">
      <label className="settings-check">
        <input
          type="checkbox"
          aria-label="启用 hedging"
          checked={v.enabled === true}
          onChange={(e) =>
            onChange({
              ...v,
              enabled: e.target.checked,
              max_inflight_attempts: v.max_inflight_attempts ?? 2,
              winner_policy: "first_valid_success",
            })
          }
        />
        启用 hedging
      </label>
      <label>
        最大并行尝试数
        <input
          type="number"
          aria-label="hedging 最大并行尝试数"
          min="1"
          max="4"
          step="1"
          value={Number(v.max_inflight_attempts ?? 1)}
          onChange={(e) =>
            onChange({ ...v, max_inflight_attempts: Number(e.target.value) })
          }
        />
      </label>
      <p className="muted">
        等待较慢时尝试其他渠道，以首个有效成功响应为准。设为 1
        时不发起并行尝试。并行请求可能增加上游消费。
      </p>
    </div>
  );
}
