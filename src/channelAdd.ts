import type { ModelAlias } from "./ModelAliases";

export interface ChannelAddDraft {
  source: string;
  key: string;
  aliases: ModelAlias[];
  position: number;
  perModel: boolean;
  modelPositions: Record<string, number>;
  error?: string;
}

export interface SiteChannelAddDraft extends ChannelAddDraft {
  modelChoices: Record<string, boolean>;
  protocols: Record<string, string>;
  compactionEnabled: boolean;
}

export interface ConfiguredChannelAddDraft extends ChannelAddDraft {
  models: string[];
  protocols?: Record<string, string>;
  editingProvider?: string;
  revision?: string;
}

export interface ChannelAddRequest<D> {
  id: string;
  action?: "save";
  label: string;
  path: "/v1/sub2api/channels" | "/v1/channel-management";
  body: string;
  draft: D;
  refresh: () => void;
}

export type SubmitChannelAdd<D> = (request: ChannelAddRequest<D>) => boolean;
