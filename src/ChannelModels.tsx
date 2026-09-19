import { Check } from "lucide-react";
import { useSubAccounts } from "./sub2apiAccounts";
import { availableModelChecks } from "./sub2apiResults";
import type { SubImportsQuery } from "./sub2apiImports";
import { boundGroups } from "./sub2apiImports";
import type { Channel } from "./types";
import { Spinner } from "./ui";
import { time } from "./format";
import { pricePair } from "./sub2apiPriceCheck";

export function ChannelModels({
  row,
  imports,
  catalog,
}: {
  row: Channel;
  imports: SubImportsQuery;
  catalog: Channel[];
}) {
  const installed = imports.data?.data.find(
    (channel) =>
      channel.source_id === row.source_id && channel.provider === row.provider,
  );
  const accounts = useSubAccounts(!!installed);
  const groups = boundGroups(installed);
  const targets = groups.flatMap(group => accounts.data?.data.find(a => a.id === group.account_id)?.targets.filter(t => t.group_id === group.group_id) || []);
  const target = targets[0];
  const models = [...new Map(targets.flatMap(availableModelChecks).sort((a,b) => (a.result?.checked_at || 0) - (b.result?.checked_at || 0)).map(check => [check.model, check])).values()];
  const checking = groups.some(group => accounts.data?.data.some(a => a.id === group.account_id && ["queued", "running"].includes(a.state)));
  const sourceUnavailable = imports.data?.unavailable_sources?.some(
    (source) => source === row.source_id || source === row.source_name,
  );
  const configured = [
    ...new Set(
      catalog
        .filter(
          (channel) =>
            channel.source_id === row.source_id &&
            channel.provider === row.provider &&
            channel.eligible,
        )
        .map((channel) => channel.model),
    ),
  ];
  if (
    (!installed || (installed.kind === "configured" && installed.binding_status !== "matched")) &&
    !imports.isPending &&
    !imports.isError &&
    !sourceUnavailable
  )
    return (
      <section className="detail-section detail-models" aria-label="可用模型">
        <h3>
          可用模型<span className="count-badge">{configured.length}</span>
        </h3>
        <p className="muted">渠道配置</p>
        {configured.length ? (
          <ul className="detail-model-list">
            {configured.map((model) => (
              <li key={model}>
                <Check size={15} className="detail-model-check" />
                <div>
                  <strong>{model}</strong>
                </div>
                <span className="detail-model-added">已配置</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">当前没有可用的已配置模型。</p>
        )}
      </section>
    );
  const loading = imports.isPending || (!!installed && accounts.isPending);
  const error =
    imports.error || sourceUnavailable || (!!installed && accounts.error);
  return (
    <section className="detail-section detail-models" aria-label="可用模型">
      <h3>
        可用模型
        {!loading && !error && target && (
          <span className="count-badge">{models.length}</span>
        )}
      </h3>
      {!loading && !error && target && (
        <p className="muted">sub2api 检测通过{installed?.kind === "configured" && " · 关联分组"}</p>
      )}
      {error ? (
        <div className="detail-models-message" role="alert">
          <span>可用模型暂时无法读取</span>
          <button
            className="button small"
            onClick={() =>
              void (imports.error || sourceUnavailable
                ? imports.refetch()
                : accounts.refetch())
            }
          >
            重试
          </button>
        </div>
      ) : loading ? (
        <div className="detail-models-message" role="status">
          <Spinner small />
          正在读取可用模型…
        </div>
      ) : !target ? (
        <p className="muted">未找到对应的 sub2api 检测记录。</p>
      ) : models.length ? (
        <ul className="detail-model-list">
          {models.map((check) => (
            <li key={check.model}>
              <Check size={15} className="detail-model-check" />
              <div>
                <strong>{check.model}</strong>
                <small
                  title={
                    check.result
                      ? new Date(
                          check.result.checked_at * 1000,
                        ).toLocaleString()
                      : undefined
                  }
                >
                  最近检测 {time(check.result?.checked_at)}
                </small>
                {installed?.kind === "configured" && groups.length === 1 && check.result?.availability.usage?.status === "matched" && (
                  <small title="关联分组最近一次检测的倍率前单价，美元／百万 token">
                    检测单价 {pricePair(check.result.availability.usage.input_price, check.result.availability.usage.output_price)}
                  </small>
                )}
              </div>
              <span
                className={
                  (installed?.kind === "configured" ? configured.includes(check.model) : installed?.models.includes(check.model))
                    ? "detail-model-added"
                    : "muted"
                }
              >
                {installed?.kind === "configured" ? configured.includes(check.model) ? "已配置" : "未配置" : installed?.models.includes(check.model) ? "已添加" : "未添加"}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">
          {checking
            ? "检测进行中，完成后会显示可用模型。"
            : "暂无检测通过的模型。"}
        </p>
      )}
    </section>
  );
}
