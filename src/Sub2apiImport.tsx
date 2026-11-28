import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, Trash2 } from "./icons";
import { ApiError, controlRequest } from "./api";
import { readCompleteSubTarget } from "./subTargetDetails";
import { readSummary } from "./summaryFeed";
import { useChannelImportKeys, useDefaultChannelChoice } from "./channelImportKeys";
import { useRouteKeyRequestStats } from "./keyRequestStats";
import { ChannelImportKeyFeedback } from "./ChannelImportKeyFeedback";
import { channelEngineLabel, channelProtocols } from "./channelProtocol";
import { Spinner } from "./ui";
import { LoadingIndicator } from "./LoadingIndicator";
import type { SiteChannelAddDraft, SiteCompactionMode, SubmitChannelAdd } from "./channelAdd";
import { CompactDialogHeader } from "./CompactDialog";
import { RouteWorkspaceLoading } from "./RouteWorkspaceLoading";
import { AccountForm } from "./SiteAccountForm";
import { ChannelBatchApply } from "./ChannelBatchApply";
import type { BatchPart, BatchDraft } from "./channelBatch";
import type { ConsoleSourcesQuery } from "./consoleSources";
import { toolUseFailed, modelToolUse, toolUseLabels } from "./toolUse";
import { CompactionStatus } from "./SubCompaction";
import { serviceTierName, serviceTiers, type ServiceTier } from "./serviceTierStatus";
import "./serviceTierRouting.css";
import { assessPrice } from "./sub2apiPriceCheck";
import { ModelPriceWarning } from "./Sub2apiPricing";
import { ModelQualityWarning } from "./ModelQualityWarning";
import { FingerprintVerdictBadge, fingerprintImportMismatch, fingerprintVerdict, useSiteFingerprints } from "./ModelFingerprints";
import type { KeyInfo, ModelPrice } from "./types";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { InstalledChannel, SubImportsQuery } from "./sub2apiImports";
import { boundGroups } from "./sub2apiImports";
import { ConfiguredChannelDialog, RouteModelTable } from "./ChannelRoutes";
import {
  ChannelBindingActions,
  BindingRemoveDialog,
  RemoveConfiguredBinding,
} from "./ChannelBindingActions";
import { channelBindingView } from "./channelBindingView";
import type { ChannelRoute } from "./channelRouteData";
import { providerRoutes, useAllChannelRoutes } from "./channelRouteData";
import { ModelAliases, aliasMappings } from "./ModelAliases";
import {
  ChannelModelSelection,
  splitChannelModels,
  unavailableModelChanges,
  retainedOnlyModels,
} from "./ChannelModelSelection";
import type { ModelAlias } from "./ModelAliases";
import {
  ModelPositions,
  selectedModelPositions,
  modelPositionLimit,
} from "./ModelPositions";
import type { ManagedChannel } from "./channelManagement";
import {
  modelChecks,
  groupQualityResult,
  availableModelChecks,
  importModelLabel,
  modelIsDefaultSelected,
  probeHasWarning,
} from "./sub2apiResults";
interface Options {
  provider?: string;
  supported: boolean;
  manageable: boolean;
  gateway_prompt_compaction?: boolean | null;
  compaction_ultrafast_only?: boolean;
  revision: string;
  keys: KeyInfo[];
  channels: { provider: string; model: string }[];
}
const siteLoginRequired = (message: string) =>
  /站点登录|站点会话|登录会话|重新登录|刷新凭据/.test(message);
// A selected-model page can omit sibling results as well as their receipts.
// Never initialize route defaults from either partial representation.
export function Sub2apiImport(props: Parameters<typeof Sub2apiImportForm>[0]) {
  const { account, target, close } = props;
  const needsComplete = !!(target.receipt_model || target.summary_model || target.list_summary);
  const complete = useQuery({
    queryKey: ["sub-import-models", account.id, target.group_id],
    enabled: needsComplete,
    retry: false,
    // One complete snapshot per opening. A reconnect must not remount the
    // form and discard edits; gcTime=0 forces a fresh read on the next open.
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
    queryFn: ({ signal }) => readCompleteSubTarget(account.id, target.group_id, signal),
  });
  if (!needsComplete) return <Sub2apiImportForm {...props}/>;
  if (complete.data && !complete.isFetching && !complete.isError)
    return <Sub2apiImportForm {...props} target={complete.data}/>;
  return <RouteWorkspaceLoading context={`${account.name} · ${target.name}`} close={close} nested={props.nested}
    label="读取完整模型检测结果" error={complete.error?.message} retry={() => void complete.refetch()} />;
}

function Sub2apiImportForm({
  account,
  target,
  imports,
  sources,
  prices,
  configured,
  initialEdit,
  nested = false,
  close,
  initialAdd,
  submitAdd,
}: {
  account: SubAccount;
  target: SubTarget;
  imports: SubImportsQuery;
  sources: ConsoleSourcesQuery;
  prices?: ModelPrice[];
  configured?: ManagedChannel;
  initialEdit?: InstalledChannel;
  nested?: boolean;
  close: () => void;
  initialAdd?: SiteChannelAddDraft;
  submitAdd?: SubmitChannelAdd<SiteChannelAddDraft>;
}) {
  const client = useQueryClient();
  const formId = useId();
  const protocolHeading = useRef<HTMLElement>(null);
  const destinationSources = (sources.data?.data || []).map(
    (source) => source.id,
  );
  const keyDirectories = useChannelImportKeys(destinationSources);
  const checks = useMemo(() => modelChecks(target), [target]);
  const quality = groupQualityResult(target);
  const available = availableModelChecks(target).map((check) => check.model);
  const installed = (imports.data?.data || []).filter(
    (i) =>
      i.kind !== "configured" &&
      i.account_id === account.id &&
      i.group_id === target.group_id,
  );
  const configuredChannels = (imports.data?.data || []).filter(
    (i) =>
      i.kind === "configured" &&
      boundGroups(i).some(
        (g) => g.account_id === account.id && g.group_id === target.group_id,
      ),
  );
  if (
    configured &&
    !configuredChannels.some(
      (i) =>
        i.source_id === configured.source_id &&
        i.provider === configured.provider,
    )
  )
    configuredChannels.push(configured);
  const configuredSources = [
    ...new Set(configuredChannels.map((i) => i.source_id)),
  ];
  const configuredRoutes = useAllChannelRoutes(configuredSources);
  const boundKeys = new Set(
    installed.map((i) => `${i.source_id}:${i.api_key_id}`),
  );
  let partialBindings =
    (!!imports.data?.scope_source_id && destinationSources.some(id => id !== imports.data?.scope_source_id)) ||
    imports.isPending ||
    imports.isError ||
    !!imports.data?.unavailable_sources?.length;
  configuredSources.forEach((id, index) => {
    const query = configuredRoutes[index];
    partialBindings ||=
      !query.data || query.isError || !!query.data.unavailable_keys?.length;
    for (const row of providerRoutes(
      query.data?.data || [],
      configuredChannels
        .filter((i) => i.source_id === id)
        .map((i) => i.provider),
    )) {
      boundKeys.add(`${id}:${row.api_key_id}`);
    }
  });
  const [editing, setEditing] = useState<InstalledChannel | null>(null);
  const [view, setView] = useState<"new" | "existing" | null>(initialAdd ? "new" : null);
  const [viewSource, setViewSource] = useState("");
  const [viewKey, setViewKey] = useState("");
  const viewSources = [
    ...new Map(
      [...installed, ...configuredChannels].map((i) => [
        i.source_id,
        i.source_name,
      ]),
    ).entries(),
  ];
  const activeSource = viewSources.some(([id]) => id === viewSource)
    ? viewSource
    : viewSources[0]?.[0] || "";
  const sourceBindings = installed.filter((i) => i.source_id === activeSource);
  const sourceRoutes =
    configuredRoutes[configuredSources.indexOf(activeSource)];
  const nativeRows = providerRoutes(
    sourceRoutes?.data?.data || [],
    configuredChannels
      .filter((i) => i.source_id === activeSource)
      .map((i) => i.provider),
  );
  const keyOptions = [
    ...new Map(
      [...sourceBindings, ...nativeRows].map((item) => [item.api_key_id, item]),
    ).values(),
  ].sort((a, b) => a.key_position - b.key_position);
  const activeKey = keyOptions.some((i) => i.api_key_id === viewKey)
    ? viewKey
    : keyOptions[0]?.api_key_id || "";
  const selectedKey = keyOptions.find((i) => i.api_key_id === activeKey);
  const bindings = channelBindingView(
    activeSource,
    activeKey,
    installed,
    configuredChannels,
    nativeRows,
  );
  const [editingNative, setEditingNative] = useState<{
    item: ManagedChannel;
    rows: ChannelRoute[];
  } | null>(null);
  const showForm =
    !!editing ||
    view === "new" ||
    (view === null &&
      !installed.length &&
      !configuredChannels.length &&
      !imports.isPending);
  const fingerprintQuery = useSiteFingerprints(account.id, target.group_id, showForm && !editing);
  const fingerprintLoading = !editing && fingerprintQuery.isLoading;
  const fingerprintFor = (model: string) => fingerprintVerdict(fingerprintQuery.data?.get(model));
  const [removing, setRemoving] = useState<InstalledChannel | null>(null);
  const keyRequestStats = useRouteKeyRequestStats(
    activeSource,
    !showForm && keyOptions.length > 0,
  );
  const isRemoving = (item: InstalledChannel) =>
    !!removing &&
    removing.source_id === item.source_id &&
    removing.api_key_id === item.api_key_id &&
    removing.provider === item.provider;
  const [modelChoices, setModelChoices] = useState<Record<string, boolean>>(initialAdd?.modelChoices || {});
  const editChecks = [...checks];
  for (const model of [
    ...installed.flatMap((i) => [
      ...i.models,
      ...Object.values(i.model_mappings || {}),
    ]),
    ...(editing?.models || []),
    ...Object.values(editing?.model_mappings || {}),
  ]) {
    if (!editChecks.some((c) => c.model === model))
      editChecks.push({ model, state: "idle", message: "", result: null });
  }
  const originals = editChecks
    .filter(
      (check) =>
        modelChoices[check.model] ??
        (editing
          ? editing.models.includes(check.model) &&
            (!editing.model_mappings?.[check.model] ||
              editing.model_mappings[check.model] === check.model)
          : modelIsDefaultSelected(check) &&
            assessPrice(check, prices).status !== "abnormal" &&
            !toolUseFailed(target, check.model) &&
            !fingerprintImportMismatch(fingerprintQuery.data?.get(check.model))),
    )
    .map((check) => check.model);
  const [aliases, setAliases] = useState<ModelAlias[]>(initialAdd?.aliases || []);
  const [protocolChoices, setProtocolChoices] = useState<
    Record<string, string>
  >(initialAdd?.protocols || {});
  const mapping = aliasMappings(aliases, originals);
  const models = mapping.models;
  const upstreamModels = [
    ...new Set([...originals, ...aliases.map((alias) => alias.upstream)]),
  ];
  const savedUpstreams = new Set(
    (editing?.models || []).map(
      (model) => editing?.model_mappings?.[model] || model,
    ),
  );
  const protocols = Object.fromEntries(
    upstreamModels.map((model) => {
      const probe = checks.find((check) => check.model === model)?.result
        ?.availability;
      return [
        model,
        protocolChoices[model] ||
          (probe?.status === "success" ? probe.protocol : "") ||
          (savedUpstreams.has(model) ? editing?.protocol : "") ||
          "",
      ];
    }),
  );
  const missingProtocol = upstreamModels.some((model) => !protocols[model]);
  const invalidModels = unavailableModelChanges(
    {
      ...Object.fromEntries(originals.map((m) => [m, m])),
      ...mapping.mappings,
    },
    Object.fromEntries(
      (editing?.models || []).map((m) => [
        m,
        editing?.model_mappings?.[m] || m,
      ]),
    ),
    (model) => available.includes(model),
  );
  const adding = showForm && !editing && (!initialEdit || view === "new");
  const [source, setSource] = useDefaultChannelChoice(destinationSources, adding, initialAdd?.source);
  const keyDirectory = keyDirectories[destinationSources.indexOf(source)];
  const [key, setKey] = useDefaultChannelChoice(
    keyDirectory?.data?.keys.map((key) => key.key_id) ?? [],
    adding,
    initialAdd?.key,
  );
  const [position, setPosition] = useState(initialAdd?.position ?? 1);
  const [perModel, setPerModel] = useState(initialAdd?.perModel ?? false);
  const [modelPositions, setModelPositions] = useState<Record<string, number>>(
    initialAdd?.modelPositions || {},
  );
  const activePositions = perModel ? modelPositions : {};
  const initializedPositions = useRef("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(initialAdd?.error || "");
  const [relogin, setRelogin] = useState(false);
  const [success, setSuccess] = useState("");
  const [compactionMode, setCompactionMode] = useState<SiteCompactionMode>(
    initialAdd?.compactionMode ?? "auto",
  );
  const [tierOverride, setTierOverride] = useState(() => ({
    destination: JSON.stringify([initialAdd?.source, initialAdd?.key]),
    choices: initialAdd?.serviceTierChoices || {},
  }));
  const tierDestination = JSON.stringify([source, key]);
  const tierChoices = tierOverride.destination === tierDestination ? tierOverride.choices : {};
  const tierPreviewBody = JSON.stringify({
    account_id: account.id, group_id: target.group_id, source_id: source, api_key_id: key,
    models: originals, model_mappings: mapping.mappings, protocols,
  });
  const checkTiers = adding && !!source && !!key && models.length > 0 && !mapping.error &&
    !missingProtocol && Object.values(protocols).includes("responses");
  const [settledTierBody, setSettledTierBody] = useState("");
  useEffect(() => {
    if (!checkTiers) return;
    const timer = window.setTimeout(() => setSettledTierBody(tierPreviewBody), 180);
    return () => window.clearTimeout(timer);
  }, [checkTiers, tierPreviewBody]);
  const tierPreview = useQuery({
    queryKey: ["service-tier-import-options", tierPreviewBody],
    enabled: checkTiers && settledTierBody === tierPreviewBody,
    queryFn: ({ signal }) => controlRequest<{
      verified: Partial<Record<ServiceTier, number>>;
      site_supported?: Partial<Record<ServiceTier, number>>;
      verification_unavailable?: boolean;
      total: number;
    }>(
      "/v1/sub2api/channel-tier-options", {method: "POST", signal, body: tierPreviewBody}),
    retry: false, staleTime: 15000, refetchOnWindowFocus: false,
  });
  const verifiedTierCount = (tier: ServiceTier) => !checkTiers || tierPreview.isError ? 0 : tierPreview.data?.verified?.[tier] || 0;
  const siteTierCount = (tier: ServiceTier) => !checkTiers || tierPreview.isError ? 0 : tierPreview.data?.site_supported?.[tier] || 0;
  const tierAvailable = (tier: ServiceTier) => verifiedTierCount(tier) > 0 || siteTierCount(tier) > 0;
  const tierIsEnabled = (tier: ServiceTier) => tierAvailable(tier) && tierChoices[tier] !== false;
  const tierSelection = (basis: "verified" | "site") => Object.fromEntries(serviceTiers
    .filter(tier => basis === "verified" ? verifiedTierCount(tier) > 0 : verifiedTierCount(tier) === 0 && siteTierCount(tier) > 0)
    .map(tier => [tier, tierIsEnabled(tier)]));
  const waitingForTiers = checkTiers && !tierPreview.data && !tierPreview.isError;
  const options = useQuery({
    queryKey: ["sub-import-options", source, key, account.id, target.group_id, adding],
    queryFn: ({ signal }) =>
      controlRequest<Options>(
        "/v1/sub2api/channel-options?" +
          new URLSearchParams({
            source_id: source,
            api_key_id: key,
            account_id: account.id,
            group_id: String(target.group_id),
            ...(adding && { include_compaction_mode: "true" }),
          }),
        { signal },
      ),
    enabled: showForm && !!source && !!key,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  const gatewayCompaction = options.data?.gateway_prompt_compaction;
  const siteNativeCompaction = target.compaction?.status === "supported";
  const autoExcludesCompaction = gatewayCompaction === false && !siteNativeCompaction;
  const compactionScopeUnavailable = compactionMode === "ultrafast" &&
    options.data?.compaction_ultrafast_only !== true;
  const importStatus = useQuery({
    queryKey: ["sub-import-status", account.id],
    queryFn: async ({ signal }) => {
      try {
        const status = await controlRequest<{
          state: string;
          job_kind: string;
          can_import: boolean;
          message?: string;
        }>(
          `/v1/sub2api/accounts/${encodeURIComponent(account.id)}/import-status`,
          { signal },
        );
        if (typeof status.can_import !== "boolean")
          throw new Error("账号状态读取失败");
        return status;
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 404) throw error;
        // The frontend may roll out before the new backend. The existing
        // headers-only summary is also local and carries the account job state.
        const { summary, legacy } = await readSummary<
          { account?: SubAccount; position: number },
          { data: SubAccount[] }
        >("/v1/sub2api/accounts", undefined, signal, "accounts");
        const current = summary
          ? summary.rows.find((row) => row.value.account?.id === account.id)
              ?.value.account
          : legacy?.data.find((row) => row.id === account.id);
        if (!current || typeof current.state !== "string") throw error;
        const canImport =
          current.state !== "running" && current.state !== "queued";
        return {
          state: current.state,
          job_kind: current.job_kind || "",
          can_import: canImport,
          message: canImport
            ? ""
            : "账号任务进行中，完成后可添加渠道。当前选择会保留。",
        };
      }
    },
    enabled: showForm && !editing && !busy,
    retry: false,
    staleTime: 0,
    refetchInterval: 5000,
  });
  const importBlocked =
    importStatus.data?.can_import !== true || importStatus.isError;
  const accountWasBusy = useRef(false);
  useEffect(() => {
    if (!showForm || editing || busy || !importStatus.data) return;
    if (!importStatus.data.can_import) {
      accountWasBusy.current = true;
    } else if (accountWasBusy.current) {
      accountWasBusy.current = false;
      setError((message) =>
        /账号正在|账号任务状态已变化|账号不存在或有任务进行中/.test(message) ? "" : message,
      );
      // Refresh availability and destination revision after waiting, without
      // replacing the user's models, aliases, destination or position.
      for (const name of ["sub2api", "sub-import-options", "sub-group-access"])
        void client.invalidateQueries({ queryKey: [name] });
    }
  }, [showForm, editing, busy, importStatus.data, client]);
  const groupAccess = useQuery({
    queryKey: ["sub-group-access", account.id, target.group_id],
    queryFn: ({ signal }) =>
      controlRequest<{ available: boolean; status: string; message?: string }>(
        `/v1/sub2api/accounts/${encodeURIComponent(account.id)}/groups/${target.group_id}/access`,
        { signal },
      ),
    enabled: showForm && !editing,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  const groupUnavailable =
    groupAccess.data?.available === false ||
    (!groupAccess.data && target.active === false);
  const groupAccessMessage = groupUnavailable
    ? groupAccess.data?.message ||
      target.message ||
      "站点当前未开放该分组，请重新检查分组状态。"
    : groupAccess.error
      ? "暂时无法核对站点分组状态，请重新检查。"
      : "";
  const visibleError =
    error || sources.error?.message || options.error?.message || "";
  useEffect(() => {
    if (
      !editing ||
      !options.data ||
      options.isFetching ||
      editing.revision !== options.data.revision
    )
      return;
    const id = `${editing.provider}:${editing.revision}`;
    if (initializedPositions.current === id) return;
    const counts: Record<string, number> = {},
      current: Record<string, number> = { ...editing.positions };
    for (const c of options.data.channels) {
      counts[c.model] = (counts[c.model] || 0) + 1;
      if (c.provider === editing.provider) current[c.model] = counts[c.model];
    }
    setModelPositions(current);
    initializedPositions.current = id;
  }, [editing, options.data, options.isFetching]);
  const positions = models.length
    ? Math.min(
        ...models.map(
          (model) =>
            new Set(
              options.data?.channels
                .filter(
                  (c) =>
                    c.model === model && c.provider !== options.data?.provider,
                )
                .map((c) => c.provider) || [],
            ).size + 1,
        ),
      )
    : 1;
  const validPosition = Math.min(position, positions);
  const duplicate = installed.find(
    (i) => i.source_id === source && i.api_key_id === key,
  );
  const stale =
    !!editing && !!options.data && editing.revision !== options.data.revision;
  function batchDraft(part: BatchPart): BatchDraft {
    const originalModels = Object.fromEntries(originals.map((m) => [m, m]));
    const desired = { ...originalModels, ...mapping.mappings };
    const saved = Object.fromEntries(
      (editing?.models || []).map((m) => [
        m,
        editing?.model_mappings?.[m] || m,
      ]),
    );
    return {
      part,
      name: `${account.name} / ${target.name}`,
      scope: { kind: "site", account: account.id, group: target.group_id },
      protocols,
      originals: originalModels,
      aliases: mapping.mappings,
      models: desired,
      retainedOnly: retainedOnlyModels(desired, saved, (m) =>
        available.includes(m),
      ),
      positions: selectedModelPositions(models, activePositions, validPosition),
      anchor: {
        source: editing?.source_id || activeSource,
        key: editing?.api_key_id || activeKey,
        provider: editing?.provider || "",
        revision: editing?.revision || "",
      },
    };
  }
  function batchButton(part: BatchPart, section?: string) {
    if (!editing) return undefined;
    return (
      <ChannelBatchApply
        section={section}
        draft={() => batchDraft(part)}
        disabled={
          busy ||
          invalidModels.length > 0 ||
          stale ||
          options.isFetching ||
          options.isError ||
          !options.data?.manageable ||
          !source ||
          !key ||
          (part !== "models" && !!mapping.error) ||
          ((part === "all" || part === "positions") && !models.length)
        }
        onApplied={() => {
          setEditing(null);
          setView("existing");
          setSuccess("批量操作结果已更新，请核对各来源接入状态。");
        }}
      />
    );
  }
  function edit(item: InstalledChannel) {
    const protocol = channelProtocols.find(
      (spec) => spec.engine === item.engine,
    )?.protocol;
    setProtocolChoices(
      protocol
        ? Object.fromEntries(
            item.models.map((model) => [
              item.model_mappings?.[model] || model,
              protocol,
            ]),
          )
        : {},
    );
    initializedPositions.current = "";
    setEditing(item);
    setView("existing");
    setRemoving(null);
    setSource(item.source_id);
    setKey(item.api_key_id);
    const saved = splitChannelModels(
      item.models.map((model) => ({
        model,
        upstream_model: item.model_mappings?.[model],
      })),
    );
    setModelChoices(
      Object.fromEntries(
        [...new Set([...checks.map((c) => c.model), ...item.models])].map(
          (model) => [model, saved.originals.includes(model)],
        ),
      ),
    );
    setAliases(saved.aliases);
    setPosition(1);
    setPerModel(true);
    setModelPositions({ ...item.positions });
    setError("");
    setSuccess("");
    void client.invalidateQueries({ queryKey: ["sub-import-options"] });
  }
  function add() {
    setProtocolChoices({});
    setCompactionMode("auto");
    setEditing(null);
    setView("new");
    setRemoving(null);
    setSource(null);
    setKey(null);
    setModelChoices({});
    setAliases([]);
    setPosition(1);
    setPerModel(false);
    setModelPositions({});
    setError("");
    setSuccess("");
  }
  useEffect(() => {
    if (initialEdit) {
      edit(initialEdit);
      setViewSource(initialEdit.source_id);
      setViewKey(initialEdit.api_key_id);
    }
    // The drawer's selected binding seeds the form once; refetches must not
    // overwrite model/alias changes that the user is already editing.
  }, []);
  async function refresh() {
    await Promise.all(
      [
        "sub2api-imports",
        "catalog",
        "channel-controls",
        "control-catalog",
        "sub2api",
        "sub-import-options",
        "channel-routes",
        "channel-management",
        "service-tier-members",
        "detail-tier-target",
      ].map((name) => client.invalidateQueries({ queryKey: [name] })),
    );
  }
  async function mutate(
    action: "add" | "replace" | "delete",
    item?: InstalledChannel,
  ) {
    if (
      busy ||
      (action === "add" && (groupUnavailable || importBlocked || fingerprintLoading || waitingForTiers || duplicate || compactionScopeUnavailable || !options.data?.supported)) ||
      (action !== "delete" &&
        (mapping.error ||
          invalidModels.length ||
          missingProtocol ||
          !models.length ||
          models.some(m => (activePositions[m] ?? validPosition) > modelPositionLimit(m, options.data?.channels || [], options.data?.provider)) ||
          options.isFetching ||
          options.isError ||
          !options.data?.keys.some((k) => k.key_id === key) ||
          !keyDirectory?.data?.keys.some((k) => k.key_id === key)))
    )
      return;
    const restrictCompaction = action === "add" && compactionMode === "ultrafast";
    const compactionEnabled = compactionMode === "auto"
      ? autoExcludesCompaction ? false : undefined
      : compactionMode !== "disabled";
    const path = restrictCompaction
      ? "/v1/sub2api/channels/ultrafast-compaction"
      : "/v1/sub2api/channels";
    const body = JSON.stringify({
      account_id: account.id,
      group_id: target.group_id,
      source_id: item?.source_id || source,
      api_key_id: item?.api_key_id || key,
      ...(action === "add" ? (compactionEnabled === undefined ? {} : { compaction_enabled: compactionEnabled }) : {
        action, provider: item?.provider || editing?.provider,
      }),
      ...(action === "add" && serviceTiers.some(tierAvailable) && {
        service_tier_enabled: tierSelection("verified"),
        service_tier_site_choices: tierSelection("site"),
      }),
      ...(action !== "delete" && {
        models: originals,
        protocols,
        ...(aliases.length || action === "replace" ? { model_mappings: mapping.mappings } : {}),
        position: validPosition,
        positions: selectedModelPositions(models, activePositions, validPosition),
      }),
      revision: item?.revision || options.data?.revision,
    });
    if (action === "add" && submitAdd) {
      if (!submitAdd({
        id: JSON.stringify(["site", account.id, target.group_id, source, key]),
        label: `${account.name} / ${target.name}`,
        path,
        body,
        draft: {
          source, key, aliases, position, perModel, modelPositions, protocols, compactionMode,
          serviceTierChoices: tierChoices,
          modelChoices: Object.fromEntries(checks.map(check => [check.model, originals.includes(check.model)])),
        },
        refresh: () => void refresh(),
      })) setError("此渠道已有添加请求在处理，请等待回执。");
      return;
    }
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const result = await controlRequest<{ message: string }>(
        path,
        { method: action === "add" ? "POST" : "PATCH", signal: AbortSignal.timeout(60000), body },
      );
      setSuccess(result.message);
      setViewSource(item?.source_id || source);
      setViewKey(item?.api_key_id || key);
      setEditing(null);
      setView("existing");
      setRemoving(null);
      setSource(null);
      setKey(null);
      // The write acknowledgement completes the user's action. Background
      // reconciliation must not hold the modal open (account reads can be slow).
      if (action !== "delete") close();
      void refresh();
    } catch (e) {
      const message = e instanceof Error ? e.message : "操作失败";
      setError(message);
      // A failed request must release the editor immediately. These reads can
      // take a minute when a source is slow; they must not keep recovery locked.
      // Site authentication fails before a destination write, so no route
      // refresh is needed in that case.
      if (action === "add" && /账号正在|账号任务状态已变化|账号不存在或有任务进行中/.test(message)) {
        accountWasBusy.current = true;
        void importStatus.refetch();
      } else if (!siteLoginRequired(message)) void refresh();
      if (action === "add" && message.includes("分组"))
        void groupAccess.refetch();
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open && !busy) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay
          className={`dialog-overlay${nested ? " route-edit-overlay" : ""}`}
        />
        <Dialog.Content
          className={`guide-dialog compact-dialog sub-import-dialog route-workspace channel-import-workspace${nested ? " route-edit-dialog" : ""}`}
          onEscapeKeyDown={(e) => {
            if (busy) e.preventDefault();
          }}
          onPointerDownOutside={(e) => {
            if (busy) e.preventDefault();
          }}
        >
          <CompactDialogHeader context={`${account.name} / ${target.name}`} busy={busy} closeLabel="关闭添加渠道">添加到渠道</CompactDialogHeader>
          <nav className="route-mode-tabs" aria-label="渠道接入视图">
            <button
              type="button"
              aria-pressed={!showForm}
              disabled={busy}
              onClick={() => {
                setView("existing");
                setEditing(null);
                setRemoving(null);
                setError("");
              }}
            >
              已接入 <span className="count-badge" title={partialBindings ? "部分路由尚未核对" : undefined}>{partialBindings ? "≥" : ""}{boundKeys.size}</span>
            </button>
            <button
              type="button"
              aria-pressed={showForm}
              disabled={busy}
              onClick={() => {
                if (!showForm) add();
              }}
            >
              <Plus size={14} />
              {editing ? "编辑接入" : "新增接入"}
            </button>
          </nav>
          <div className="compact-dialog-scroll route-import-body">
            {!showForm && imports.isPending && <LoadingIndicator className="route-import-loading" label="读取接入记录" />}
            {imports.error && (
              <div role="alert" className="error-banner">
                {imports.error.message}
                <button
                  className="button small"
                  disabled={busy}
                  onClick={() => void imports.refetch()}
                >
                  重新读取
                </button>
              </div>
            )}
            {!showForm && !imports.isPending && (
              <>
                <div className="route-section-heading">
                  <h3>已保存接入</h3>
                  <ChannelBatchApply
                    remove
                    onPreview={() => setView("existing")}
                    draft={() => batchDraft("delete")}
                    disabled={busy || partialBindings || !boundKeys.size}
                    onApplied={() => {
                      setEditing(null);
                      setView("existing");
                      setSuccess("批量删除结果已更新，请核对各来源接入状态。");
                    }}
                  />
                </div>
                <div className="route-destination-grid route-browse-selectors">
                  <label className="sub-import-field">
                    uni-api 来源
                    <select
                      aria-label="查看 uni-api 来源"
                      value={activeSource}
                      disabled={!viewSources.length}
                      onChange={(e) => {
                        setViewSource(e.target.value);
                        setViewKey("");
                        setRemoving(null);
                      }}
                    >
                      {!viewSources.length && (
                        <option value="">暂无已接入来源</option>
                      )}
                      {viewSources.map(([id, name]) => (
                        <option key={id} value={id}>
                          {name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="sub-import-field">
                    API key
                    <select
                      aria-label="查看 API key"
                      title="近 24 小时全部模型的最终请求成功率；每个请求经过重试后的最终结果只计一次。"
                      value={activeKey}
                      disabled={!keyOptions.length}
                      onChange={(e) => {
                        setViewKey(e.target.value);
                        setRemoving(null);
                      }}
                    >
                      {!keyOptions.length && (
                        <option value="">
                          {sourceRoutes?.isPending
                            ? "正在读取路由…"
                            : "暂无已接入的 API key"}
                        </option>
                      )}
                      {keyOptions.map((item) => (
                        <option key={item.api_key_id} value={item.api_key_id}>
                          Key {item.key_position} · {item.key_prefix}
                          {" · 近 24 小时 · "}
                          {keyRequestStats.label({
                            key_id: item.api_key_id,
                            source_id: activeSource,
                          })}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
                {sourceRoutes?.isPending && (
                  <LoadingIndicator size="compact" label="读取路由位置" />
                )}
                {(sourceRoutes?.error ||
                  !!sourceRoutes?.data?.unavailable_keys?.length) && (
                  <div role="alert" className="error-banner">
                    {sourceRoutes.error?.message ||
                      "部分 API key 路由暂不可用，当前列表不完整。"}
                    <button
                      type="button"
                      className="button small"
                      disabled={sourceRoutes.isFetching}
                      onClick={() => void sourceRoutes.refetch()}
                    >
                      重新读取路由
                    </button>
                  </div>
                )}
                {!!bindings.length && selectedKey && (
                  <section className="sub-installed route-selected-binding">
                    <article>
                      <div className="route-binding-heading">
                        <div>
                          <strong>
                            {
                              viewSources.find(
                                ([id]) => id === activeSource,
                              )?.[1]
                            }{" "}
                            · Key {selectedKey.key_position}
                          </strong>
                          <small>
                            {bindings.length} 个渠道 ·{" "}
                            {bindings.reduce((n, b) => n + b.rows.length, 0)}{" "}
                            条模型路由
                          </small>
                        </div>
                      </div>
                      <div className="route-binding-providers">
                        {bindings.map((binding) => (
                          <div
                            className="route-binding-provider"
                            key={binding.provider}
                          >
                            <div>
                              <strong>{binding.name}</strong>
                              <small>
                                {binding.installed ? "站点接入" : "配置渠道"}
                                {channelEngineLabel(
                                  binding.installed || binding.configured,
                                ) &&
                                  ` · ${channelEngineLabel(binding.installed || binding.configured)}`}{" "}
                                · {binding.rows.length} 个模型
                              </small>
                            </div>
                            <ChannelBindingActions
                              target={{
                                provider: binding.provider,
                                name: binding.name,
                                source_id: activeSource,
                                source_name: viewSources.find(
                                  ([id]) => id === activeSource,
                                )?.[1],
                                model: binding.rows[0]?.model || "",
                              }}
                              disabled={
                                busy ||
                                (!!binding.installed &&
                                  !binding.installed.manageable)
                              }
                              onEdit={() => {
                                if (binding.installed) edit(binding.installed);
                                else if (binding.configured)
                                  setEditingNative({
                                    item: {
                                      ...binding.configured,
                                      engine: "",
                                      account_ids: [account.id],
                                    },
                                    rows: binding.rows,
                                  });
                              }}
                              remove={
                                binding.installed ? (
                                  <Dialog.Root
                                    open={isRemoving(binding.installed)}
                                    onOpenChange={(open) => {
                                      if (busy) return;
                                      setRemoving(
                                        open ? binding.installed! : null,
                                      );
                                      setError("");
                                    }}
                                  >
                                    <Dialog.Trigger asChild>
                                      <button
                                        type="button"
                                        className="button small"
                                        disabled={
                                          busy || !binding.installed.manageable
                                        }
                                      >
                                        <Trash2 size={13} />
                                        删除
                                      </button>
                                    </Dialog.Trigger>
                                    {isRemoving(binding.installed) && (
                                      <BindingRemoveDialog
                                        target={binding.installed}
                                        keyPosition={
                                          binding.installed.key_position
                                        }
                                        busy={busy}
                                        close={() => setRemoving(null)}
                                        onConfirm={() =>
                                          void mutate("delete", removing!)
                                        }
                                      >
                                        {error && (
                                          <div
                                            role="alert"
                                            className="error-banner"
                                          >
                                            {error}
                                          </div>
                                        )}
                                      </BindingRemoveDialog>
                                    )}
                                  </Dialog.Root>
                                ) : (
                                  <RemoveConfiguredBinding
                                    target={{
                                      provider: binding.provider,
                                      name: binding.name,
                                      source_id: activeSource,
                                      source_name: viewSources.find(
                                        ([id]) => id === activeSource,
                                      )?.[1],
                                      model: binding.rows[0]?.model || "",
                                    }}
                                    keyId={activeKey}
                                    keyPosition={selectedKey.key_position}
                                  />
                                )
                              }
                            />
                            {binding.installed &&
                              !binding.installed.manageable && (
                                <small className="route-binding-notice">
                                  此来源需要更新 uni-api 后才能编辑和删除。
                                </small>
                              )}
                          </div>
                        ))}
                      </div>
                      <RouteModelTable
                        showChannel={bindings.length > 1}
                        rows={bindings
                          .flatMap((binding) =>
                            binding.rows.map((row) => ({
                              ...row,
                              channel: binding.name,
                            })),
                          )
                          .sort(
                            (a, b) =>
                              a.model.localeCompare(b.model) ||
                              (a.position || 0) - (b.position || 0),
                          )}
                      />
                    </article>
                  </section>
                )}
                {!bindings.length &&
                  !!viewSources.length &&
                  !sourceRoutes?.isPending &&
                  !sourceRoutes?.error && (
                    <p className="route-empty">此来源暂无已接入的路由。</p>
                  )}
                {!installed.length &&
                  !configuredChannels.length &&
                  !imports.isPending && (
                    <p className="route-empty">
                      尚未接入任何 API key，选择“新增接入”开始配置。
                    </p>
                  )}
              </>
            )}
            {success && (
              <p role="status" className="sub-import-success">
                {success}
              </p>
            )}
            {!showForm && visibleError && (
              <div role="alert" className="error-banner">
                {visibleError}
                {!!account.id && siteLoginRequired(visibleError) && (
                  <button
                    type="button"
                    className="button small"
                    disabled={busy}
                    onClick={() => setRelogin(true)}
                  >
                    重新登录站点
                  </button>
                )}
                {sources.isError && (
                  <button
                    type="button"
                    className="button small"
                    disabled={busy || sources.isFetching}
                    onClick={() => void sources.refetch()}
                  >
                    重新读取来源
                  </button>
                )}
              </div>
            )}
            {showForm && !editing && importBlocked && !importStatus.data && !importStatus.isError && (
              <LoadingIndicator size="compact" label="核对账号任务状态" />
            )}
            {showForm && !editing && importBlocked && (importStatus.data || importStatus.isError) && (
              <div
                role="status"
                aria-label="账号任务状态"
                className="sub-import-note"
              >
                {importStatus.isError
                  ? "暂时无法核对账号任务状态。"
                  : importStatus.data?.message || "账号任务进行中，完成后可添加渠道。"}
                {importStatus.isError && (
                  <button
                    type="button"
                    className="button small"
                    disabled={importStatus.isFetching || busy}
                    onClick={() => void importStatus.refetch()}
                  >
                    重新读取账号状态
                  </button>
                )}
              </div>
            )}
            {showForm && !editing && groupAccessMessage && (
              <div role="alert" className="error-banner">
                {groupAccessMessage}
                <button
                  type="button"
                  className="button small"
                  disabled={groupAccess.isFetching || busy}
                  onClick={() => {
                    setError("");
                    void groupAccess.refetch();
                  }}
                >
                  重新检查分组状态
                </button>
              </div>
            )}
            {showForm && <ChannelImportKeyFeedback directory={keyDirectory} />}
            {showForm && (
              <form
                id={formId}
                onSubmit={(e) => {
                  e.preventDefault();
                  if (!stale && !duplicate && !editing) void mutate("add");
                  else if (editing && !stale) void mutate("replace", editing);
                }}
              >
                <div className="route-destination-grid">
                  <label className="sub-import-field">
                    uni-api 来源
                    <select
                      aria-label="添加到 uni-api 来源"
                      required
                      value={source}
                      disabled={busy || !!editing || !sources.data?.data.length}
                      onChange={(e) => {
                        setSource(e.target.value);
                        setKey(null);
                        setPosition(1);
                        setPerModel(false);
                        setModelPositions({});
                        setError("");
                      }}
                    >
                      <option value="" disabled>
                        {sources.data?.data.length
                          ? "选择来源"
                          : sources.isPending
                            ? "正在读取来源…"
                            : sources.isError
                              ? "来源加载失败"
                              : "暂无 uni-api 来源"}
                      </option>
                      {sources.data?.data.map((s) => (
                        <option key={s.id} value={s.id}>
                          {s.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="sub-import-field">
                    API key
                    <select
                      aria-label="添加到 API key"
                      required
                      value={key}
                      disabled={
                        !source ||
                        busy ||
                        !!editing ||
                        !keyDirectory?.data?.keys.length
                      }
                      onChange={(e) => {
                        setKey(e.target.value);
                        setPosition(1);
                        setPerModel(false);
                        setModelPositions({});
                      }}
                    >
                      <option value="" disabled>
                        {!source
                          ? "请先选择来源"
                          : keyDirectory?.data
                            ? keyDirectory.data.keys.length
                              ? "选择 API key"
                              : "暂无 API key"
                            : keyDirectory?.isError
                              ? "API key 加载失败"
                              : "正在读取 API key…"}
                      </option>
                      {keyDirectory?.data?.keys.map((k) => (
                        <option key={k.key_id} value={k.key_id}>
                          Key {k.position} · {k.prefix}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="sub-import-field">
                    请求位置
                    <select
                      aria-label="渠道添加位置"
                      aria-busy={!!key && options.isFetching}
                      value={perModel ? "per-model" : validPosition}
                      disabled={
                        !key ||
                        !options.data ||
                        options.isFetching ||
                        options.isError ||
                        busy
                      }
                      onChange={(e) => {
                        if (e.target.value === "per-model") {
                          setPerModel(true);
                        } else {
                          setPosition(Number(e.target.value));
                          setPerModel(false);
                          setModelPositions({});
                        }
                      }}
                    >
                      <option value="per-model">{key && options.isFetching ? "核对路由中…" : "逐模型微调"}</option>
                      {Array.from({ length: positions }, (_, i) => (
                        <option key={i} value={i + 1}>
                          {key && options.isFetching && i + 1 === validPosition ? "核对路由中…" : `第 ${i + 1} 位`}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
                <ChannelModelSelection
                  models={editChecks.map((c) => c.model)}
                  selected={originals}
                  editing={!!editing}
                  disabled={busy || (!editing && (groupUnavailable || fingerprintLoading))}
                  actions={batchButton("models", "模型勾选")}
                  canSelect={(model) => available.includes(model)}
                  onChange={(selected) => setModelChoices(previous => ({
                    ...previous,
                    ...Object.fromEntries(editChecks
                      .filter(({ model }) => selected.includes(model) !== originals.includes(model))
                      .map(({ model }) => [model, selected.includes(model)])),
                  }))}
                  status={(model) => {
                    const check = editChecks.find((c) => c.model === model)!;
                    return (
                      <>
                        {!editing && fingerprintImportMismatch(fingerprintQuery.data?.get(model)) && (
                          <FingerprintVerdictBadge
                            kind="mismatch"
                            prefix={fingerprintImportMismatch(fingerprintQuery.data?.get(model)) === "provisional" ? "暂判" : undefined}
                            probability={fingerprintFor(model)?.probability ?? null}
                          />
                        )}
                        <ModelQualityWarning model={model} result={quality} />
                        {toolUseFailed(target, model) && (
                          <small className="negative">
                            Tool use ·{" "}
                            {toolUseLabels[modelToolUse(target, model)!.status]}
                          </small>
                        )}
                        <ModelPriceWarning check={check} prices={prices} />
                        {(!available.includes(model) ||
                          probeHasWarning(check.result?.availability)) && (
                          <small
                            className={
                              probeHasWarning(check.result?.availability)
                                ? "completion-warning"
                                : undefined
                            }
                          >
                            {importModelLabel(check)}
                          </small>
                        )}
                      </>
                    );
                  }}
                />
                <ModelAliases
                  actions={batchButton("aliases", "模型重命名")}
                  canSelect={(model) => available.includes(model)}
                  models={[
                    ...new Set([
                      ...(editing ? editChecks.map((c) => c.model) : available),
                      ...Object.values(editing?.model_mappings || {}),
                    ]),
                  ]}
                  aliases={aliases}
                  onChange={setAliases}
                  disabled={busy}
                />
                {!!invalidModels.length && (
                  <p role="alert" className="negative">
                    新增模型须检测可用：{invalidModels.join("、")}
                  </p>
                )}
                {mapping.error && (
                  <p role="alert" className="negative">
                    {mapping.error}
                  </p>
                )}
                {!editing && (
                  <div className="sub-import-field">
                    <div className="service-tier-import-toggles" role="group" aria-label="渠道能力">
                    <label className="settings-check compaction-import-mode">
                      远程压缩策略
                      <select value={compactionMode} disabled={busy} aria-describedby={formId + "-compaction"}
                        onChange={event => setCompactionMode(event.target.value as SiteCompactionMode)}>
                        <option value="auto">自动（默认）</option>
                        <option value="enabled">开启</option>
                        <option value="disabled">关闭</option>
                        <option value="ultrafast" disabled={options.data?.compaction_ultrafast_only !== true}>仅 Ultrafast</option>
                      </select>
                    </label>
                    {serviceTiers.map(tier => <label className="settings-check" key={tier}>
                      <input type="checkbox" aria-label={`开启 ${serviceTierName[tier]}`}
                        checked={tierIsEnabled(tier)} disabled={busy || !tierAvailable(tier)}
                        onChange={event => setTierOverride({destination:tierDestination, choices:{...tierChoices, [tier]:event.target.checked}})}/>
                      {serviceTierName[tier]}
                      <small className={tierAvailable(tier) ? "positive" : "muted"}>
                        {verifiedTierCount(tier) ? `已验证 ${verifiedTierCount(tier)}/${tierPreview.data?.total}` :
                          siteTierCount(tier) ? "分组已验证支持" :
                          waitingForTiers ? "核对中…" : tierPreview.isError || tierPreview.data?.verification_unavailable ? "未能验证" : "未验证"}
                      </small>
                    </label>)}
                    </div>
                    <small id={formId + "-compaction"} className="muted">
                      自动：网关支持适配或能力未确认时不排除压缩；确认是旧网关时沿用站点原生检测结果。此策略不影响普通请求。
                    </small>
                    <div className="compaction-import-evidence muted">
                      <small>站点原生检测：</small><CompactionStatus target={target} />
                      {target.compaction?.model && <small className="compaction-tested-model">检测模型：{target.compaction.model}</small>}
                      <small>网关压缩适配：{gatewayCompaction === true ? "支持" : gatewayCompaction === false ? "不支持" : "未确认"}</small>
                    </div>
                    <small className={`compaction-import-policy ${compactionScopeUnavailable ? "negative" : "muted"}`} role="status">
                      {compactionScopeUnavailable ? "此来源不支持仅 Ultrafast，请调整策略或切换来源。"
                        : compactionMode !== "auto" ? "\u00a0"
                        : autoExcludesCompaction ? "自动：此旧网关将排除压缩。"
                        : "自动：不写入压缩排除规则。"}
                    </small>
                  </div>
                )}
                <ModelPositions
                  actions={batchButton("positions", "路由位置")}
                  models={models}
                  channels={options.data?.channels || []}
                  provider={options.data?.provider}
                  positions={activePositions}
                  defaultPosition={validPosition}
                  onChange={setModelPositions}
                  disabled={
                    !key ||
                    !options.data ||
                    options.isFetching ||
                    options.isError ||
                    busy ||
                    !perModel
                  }
                  uniform={!perModel}
                />
                {source && options.data && !options.data.supported && (
                  <div role="alert" className="error-banner">
                    来源尚不支持临时添加渠道，请更新 uni-api。
                  </div>
                )}
                {stale && (
                  <div role="alert" className="error-banner">
                    渠道配置已变化，请重新读取后点击编辑。
                    <button
                      type="button"
                      className="button small"
                      disabled={busy}
                      onClick={() => {
                        setEditing(null);
                        setView("existing");
                        void imports.refetch();
                      }}
                    >
                      重新读取
                    </button>
                  </div>
                )}
                {!editing && duplicate && (
                  <div role="status">
                    已添加到此 API key。
                    <button
                      type="button"
                      className="button small"
                      disabled={busy || !duplicate.manageable}
                      onClick={() => edit(duplicate)}
                    >
                      编辑现有渠道
                    </button>
                  </div>
                )}
                {!!upstreamModels.length && <section ref={protocolHeading} tabIndex={-1} className="import-protocols" aria-label={editing ? "编辑临时渠道" : "上游协议"}>
                  <div className="route-section-heading"><h3>{editing ? "编辑临时渠道" : "上游协议"}</h3>
                    {missingProtocol && <span className="negative">有模型尚未选择协议</span>}
                  </div>
                  <div className="import-protocol-labels" aria-hidden="true"><span>模型</span><span>上游协议</span></div>
                  {upstreamModels.map(model => <label key={model} className="import-protocol-row">
                    <span title={model}>{model}</span>
                    <select aria-label={`${model} 上游协议`} value={protocols[model] || ""} disabled={busy}
                      onChange={event => setProtocolChoices(old => ({ ...old, [model]: event.target.value }))}>
                      <option value="">选择协议</option>
                      {channelProtocols.map(spec => <option key={spec.protocol} value={spec.protocol}>{spec.label} · {spec.endpoint}</option>)}
                    </select>
                  </label>)}
                </section>}
              </form>
            )}
          </div>
          {showForm && <div className="compact-dialog-footer import-footer">
                  {visibleError && <div role="alert" className="error-banner import-error">
                    {visibleError}
                    {!!account.id && siteLoginRequired(visibleError) && <button type="button" className="button small" disabled={busy} onClick={() => setRelogin(true)}>重新登录站点</button>}
                    {sources.isError && <button type="button" className="button small" disabled={busy || sources.isFetching} onClick={() => void sources.refetch()}>重新读取来源</button>}
                  </div>}
                  {batchButton("all")}
                  {missingProtocol && <button type="button" className="button small ghost" onClick={() => protocolHeading.current?.focus()}>选择协议</button>}
                  <button
                    className="button"
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      if (!nested && installed.length) {
                        setEditing(null);
                        setView("existing");
                      } else close();
                    }}
                  >
                    取消
                  </button>
                  <button
                    className="button primary"
                    type="submit"
                    form={formId}
                    disabled={
                      busy ||
                      (!editing && (groupUnavailable || importBlocked || waitingForTiers || compactionScopeUnavailable)) ||
                      !source ||
                      !key ||
                      !keyDirectory?.data?.keys.some((k) => k.key_id === key) ||
                      !options.data?.keys.some((k) => k.key_id === key) ||
                      !!mapping.error ||
                      invalidModels.length > 0 ||
                      missingProtocol ||
                      !models.length ||
                      !options.data?.supported ||
                      options.isFetching ||
                      options.isError ||
                      fingerprintLoading ||
                      models.some(
                        (m) =>
                          (activePositions[m] ?? validPosition) >
                          modelPositionLimit(
                            m,
                            options.data?.channels || [],
                            options.data?.provider,
                          ),
                      ) ||
                      stale ||
                      (!editing && !!duplicate) ||
                      (!!editing && !options.data?.manageable)
                    }
                  >
                    {busy ? <Spinner small /> : <Plus size={15} />}
                    {editing ? "保存更改" : "添加到渠道"}
                  </button>
          </div>}
          {relogin && (
            <AccountForm
              initial={account}
              sessionOnly
              close={() => setRelogin(false)}
              saved={() => {
                setRelogin(false);
                setError("");
                setSuccess(
                  "站点登录已更新，已保留当前选择，请继续添加到渠道。",
                );
                void options.refetch();
                void client.invalidateQueries({
                  queryKey: ["sub2api"],
                  refetchType: "none",
                });
              }}
            />
          )}
          {editingNative && (
            <ConfiguredChannelDialog
              item={editingNative.item}
              prices={prices}
              batchScope={{
                kind: "site",
                account: account.id,
                group: target.group_id,
              }}
              initialEdit={editingNative.rows}
              close={() => setEditingNative(null)}
            />
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
