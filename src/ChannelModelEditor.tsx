import * as Dialog from "@radix-ui/react-dialog";
import { useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { ConfiguredChannelDialog } from "./ChannelRoutes";
import { Sub2apiImport } from "./Sub2apiImport";
import { useSubAccounts } from "./sub2apiAccounts";
import { useConsoleSources } from "./consoleSources";
import { useSubPrices } from "./Sub2apiPricing";
import {
  groupManagedChannels,
  useChannelManagement,
} from "./channelManagement";
import type { ManagedChannel } from "./channelManagement";
import type { ChannelRoute } from "./channelRouteData";
import type { InstalledChannel } from "./sub2apiImports";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import { routeOptions } from "./channelRouteData";
import type { SubImportsQuery } from "./sub2apiImports";
import type { Channel } from "./types";
import { Spinner } from "./ui";

// The drawer owns only navigation. All editing, validation and batch actions
// remain in the same dialogs used by Channel Management.
export function ChannelModelEditor({
  row,
  imports,
  keyId,
  session,
  close,
}: {
  row: Channel;
  imports: SubImportsQuery;
  keyId: string;
  session: string;
  close: () => void;
}) {
  // Once open, background discovery errors must not unmount the form or erase
  // its draft. The shared editor still fences saves with fresh route revisions.
  const resolved = useRef<
    | {
        kind: "site";
        account: SubAccount;
        target: SubTarget;
        installed: InstalledChannel;
      }
    | {
        kind: "configured";
        group: ManagedChannel;
        initialEdit?: ChannelRoute[];
      }
    | null
  >(null);
  const installed = imports.data?.data.find(
    (item) =>
      item.source_id === row.source_id &&
      item.provider === row.provider &&
      item.kind !== "configured",
  );
  const sources = useConsoleSources(session);
  const accounts = useSubAccounts(!!installed);
  const prices = useSubPrices(session);
  const inventory = useChannelManagement(!installed);
  const routes = useQuery({
    ...routeOptions(row.source_id || ""),
    enabled: !!row.source_id && !installed,
  });
  const account = accounts.data?.data.find(
    (item) => item.id === installed?.account_id,
  );
  const target = account?.targets.find(
    (item) => item.group_id === installed?.group_id,
  );
  const sourceUnavailable = imports.data?.unavailable_sources?.some(
    (source) => source === row.source_id || source === row.source_name,
  );
  const loading =
    imports.isPending ||
    sources.isPending ||
    (installed ? accounts.isPending : inventory.isPending || routes.isPending);
  const error =
    imports.error ||
    sources.error ||
    (sourceUnavailable ? new Error("当前来源的接入状态未完整加载。") : null) ||
    (installed
      ? accounts.error
      : inventory.error ||
        routes.error ||
        (inventory.data?.unavailable_sources?.some(
          (source) => source === row.source_id || source === row.source_name,
        ) || routes.data?.unavailable_keys?.length
          ? new Error("当前来源的渠道或 API key 路由未完整加载。")
          : null));

  if (
    !resolved.current &&
    !loading &&
    !error &&
    installed &&
    account &&
    target
  ) {
    resolved.current = { kind: "site", account, target, installed };
  }
  if (!resolved.current && !loading && !error && !installed) {
    // Copies carry their authoritative origin in the route API. Do not infer
    // it from a display name, provider prefix, or a different source's routes.
    const exact = (routes.data?.data || []).filter(
      (route) => route.provider === row.provider,
    );
    const origin =
      exact.find((route) => route.origin_provider)?.origin_provider ||
      row.provider;
    const member = inventory.data?.data.find(
      (item) => item.source_id === row.source_id && item.provider === origin,
    );
    if (member) {
      const group = groupManagedChannels(inventory.data!.data).find((group) =>
        group.members?.some(
          (item) =>
            item.source_id === member.source_id &&
            item.provider === member.provider,
        ),
      )!;
      const separator = keyId.indexOf("::");
      const keyMatchesSource =
        separator < 0 || keyId.slice(0, separator) === row.source_id;
      const selectedKey = keyMatchesSource
        ? keyId.slice(separator < 0 ? 0 : separator + 2)
        : "";
      const caller = keyId
        ? exact.find((route) => route.api_key_id === selectedKey)?.api_key_id
        : [...exact].sort((a, b) => a.key_position - b.key_position)[0]
            ?.api_key_id;
      const initialEdit = caller
        ? exact.filter((route) => route.api_key_id === caller)
        : undefined;
      resolved.current = { kind: "configured", group, initialEdit };
    }
  }
  if (resolved.current?.kind === "site") {
    const ready = resolved.current;
    return (
      <Sub2apiImport
        account={account || ready.account}
        target={target || ready.target}
        imports={imports}
        sources={sources}
        prices={prices.data?.data}
        initialEdit={ready.installed}
        nested
        close={close}
      />
    );
  }
  if (resolved.current?.kind === "configured") {
    const ready = resolved.current;
    return (
      <ConfiguredChannelDialog
        item={ready.group}
        initialSourceId={row.source_id}
        initialEdit={ready.initialEdit}
        nested
        close={close}
      />
    );
  }
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay route-edit-overlay" />
        <Dialog.Content className="guide-dialog route-edit-dialog">
          <Dialog.Title>编辑可用模型</Dialog.Title>
          <Dialog.Description>
            {row.source_name || row.source_id} ·{" "}
            {row.provider_name || row.provider}
          </Dialog.Description>
          <Dialog.Close
            className="icon-button detail-close"
            aria-label="关闭模型编辑"
          >
            <X size={18} />
          </Dialog.Close>
          {loading ? (
            <p role="status">
              <Spinner small /> 正在读取当前渠道的已保存模型和路由…
            </p>
          ) : (
            <div role="alert">
              <p>
                {error?.message ||
                  (installed
                    ? "此渠道关联的账号或分组已不存在，请重新同步渠道管理。"
                    : "未找到当前渠道的配置，请刷新接入状态。")}
              </p>
              <button
                className="button small"
                onClick={() => {
                  void imports.refetch();
                  void sources.refetch();
                  if (installed) void accounts.refetch();
                  else {
                    void inventory.refetch();
                    void routes.refetch();
                  }
                }}
              >
                重新读取
              </button>
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
