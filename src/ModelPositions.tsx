export interface PositionChannel {
  provider: string;
  model: string;
}
export function modelPositionLimit(
  model: string,
  channels: PositionChannel[],
  provider?: string,
) {
  return (
    new Set(
      channels
        .filter((c) => c.model === model && c.provider !== provider)
        .map((c) => c.provider),
    ).size + 1
  );
}
export function selectedModelPositions(
  models: string[],
  overrides: Record<string, number>,
  fallback: number,
) {
  return Object.fromEntries(
    models.map((model) => [model, overrides[model] ?? fallback]),
  );
}
export function ModelPositions({
  models,
  channels,
  provider,
  positions,
  defaultPosition,
  onChange,
  disabled,
}: {
  models: string[];
  channels: PositionChannel[];
  provider?: string;
  positions: Record<string, number>;
  defaultPosition: number;
  onChange: (positions: Record<string, number>) => void;
  disabled?: boolean;
}) {
  return (
    <section className="model-positions">
      <h4>逐模型调整位置</h4>
      <div className="model-position-list">
        {models.map((model) => {
          const max = modelPositionLimit(model, channels, provider),
            value = positions[model] ?? defaultPosition;
          return (
            <label key={model}>
              <span>{model}</span>
              <select
                aria-label={`${model} 的路由位置`}
                value={value}
                disabled={disabled}
                onChange={(e) =>
                  onChange({ ...positions, [model]: Number(e.target.value) })
                }
              >
                {value > max && (
                  <option value={value} disabled>
                    第 {value} 位（已失效，请重选）
                  </option>
                )}
                {Array.from({ length: max }, (_, i) => (
                  <option key={i} value={i + 1}>
                    第 {i + 1} 位
                  </option>
                ))}
              </select>
            </label>
          );
        })}
      </div>
    </section>
  );
}
