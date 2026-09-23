import type { ChannelRoute } from "./channelRouteData";
import type { InstalledChannel } from "./sub2apiImports";

export interface ChannelBindingView {
  provider: string;
  name: string;
  installed?: InstalledChannel;
  configured?: InstalledChannel;
  rows: ChannelRoute[];
}

// A caller key may contain both imported and native providers. The selector
// owns the key; provider identity is retained for editing and route positions.
export function channelBindingView(
  source: string,
  key: string,
  installed: InstalledChannel[],
  configured: InstalledChannel[],
  routes: ChannelRoute[],
): ChannelBindingView[] {
  const bindings = new Map<string, ChannelBindingView>();
  for (const item of installed) {
    if (item.source_id !== source || item.api_key_id !== key) continue;
    bindings.set(item.provider, {
      provider: item.provider,
      name: item.name,
      installed: item,
      rows: item.models.map((model) => ({
        provider: item.provider,
        model,
        upstream_model: item.model_mappings?.[model],
        api_key_id: key,
        key_position: item.key_position,
        key_prefix: item.key_prefix,
        position: item.positions[model],
      })),
    });
  }
  for (const row of routes) {
    if (row.api_key_id !== key) continue;
    const item = configured.find(
      (i) =>
        i.source_id === source &&
        (i.provider === row.provider || i.provider === row.origin_provider),
    );
    if (!item) continue;
    // Imported providers already have a complete editable binding. Avoid
    // showing the same route twice when both discovery paths report it.
    if (bindings.get(row.provider)?.installed) continue;
    let binding = bindings.get(row.provider);
    if (!binding) {
      binding = {
        provider: row.provider,
        name: item.name,
        configured: item,
        rows: [],
      };
      bindings.set(row.provider, binding);
    }
    if (!binding.rows.some((r) => r.model === row.model))
      binding.rows.push(row);
  }
  return [...bindings.values()];
}
