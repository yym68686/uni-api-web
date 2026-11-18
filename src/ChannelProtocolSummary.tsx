import {
  channelEngineLabel,
  channelProtocolLabel,
  protocolForEngine,
} from "./channelProtocol";
import { channelMembers } from "./channelManagement";
import type { ManagementRow } from "./channelManagement";
import { boundGroups } from "./sub2apiImports";
import type { SubImports } from "./sub2apiImports";
import type { Routes } from "./channelRouteData";

export function ChannelProtocolSummary({
  row,
  imports,
  routes,
}: {
  row: ManagementRow;
  imports?: SubImports;
  routes?: Record<string, Routes>;
}) {
  const channels = row.configured
    ? channelMembers(row.configured)
    : (imports?.data || []).filter((c) =>
        boundGroups(c).some(
          (g) =>
            g.account_id === row.account.id &&
            g.group_id === row.target.group_id,
        ),
      );
  const descriptions = [
    ...new Set(
      channels.flatMap((c) => {
        const found = routes?.[c.source_id]?.data.filter(
          (r) =>
            r.provider === c.provider &&
            (!c.api_key_id || r.api_key_id === c.api_key_id),
        );
        return found?.length
          ? found.map(
              (r) => channelEngineLabel({ ...c, ...r }) || "实际引擎未确认",
            )
          : [channelEngineLabel(c) || "实际引擎未确认"];
      }),
    ),
  ];
  const detected = [
    ...new Set(
      (row.selected ? [row.selected] : row.checks).flatMap((c) =>
        c.result?.availability.status === "success" &&
        c.result.availability.protocol
          ? [c.result.availability.protocol]
          : [],
      ),
    ),
  ];
  const detectedLabel = detected.length ? `检测：${detected.map(channelProtocolLabel).join(" / ")}` : "";
  const details = [...descriptions, detectedLabel,
    channels.length > 1 && new Set(channels.map(c => protocolForEngine(c.engine))).size > 1
      ? "多个协议接入 · 优化时逐项核对" : ""].filter(Boolean).join("\n");
  return <small className="check-source channel-protocol-summary management-table-label" title={details}>
    {descriptions.length > 1 ? `${descriptions.length} 种引擎` : descriptions[0] || "未接入"}
  </small>;
}
