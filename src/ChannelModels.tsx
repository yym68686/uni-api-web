import { Check } from "lucide-react";
import { useSubAccounts } from "./sub2apiAccounts";
import { availableModelChecks } from "./sub2apiResults";
import type { SubImportsQuery } from "./sub2apiImports";
import type { Channel } from "./types";
import { Spinner } from "./ui";
import { time } from "./format";

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
  const account = accounts.data?.data.find(
    (item) => item.id === installed?.account_id,
  );
  const target = account?.targets.find(
    (item) => item.group_id === installed?.group_id,
  );
  const models = target ? availableModelChecks(target) : [];
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
    !installed &&
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
        <p className="muted">sub2api 检测通过</p>
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
              </div>
              <span
                className={
                  installed?.models.includes(check.model)
                    ? "detail-model-added"
                    : "muted"
                }
              >
                {installed?.models.includes(check.model) ? "已添加" : "未添加"}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">
          {account && ["queued", "running"].includes(account.state)
            ? "检测进行中，完成后会显示可用模型。"
            : "暂无检测通过的模型。"}
        </p>
      )}
    </section>
  );
}
