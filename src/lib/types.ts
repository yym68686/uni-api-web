export interface DailyUsagePoint {
  date: string;
  requests: number;
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  errorRate: number;
}

export interface UsageSummary {
  requests24h: number;
  tokens24h: number;
  errorRate24h: number;
  spend24hUsd: number;
}

export interface UsageResponse {
  summary: UsageSummary;
  daily: DailyUsagePoint[];
  topModels: Array<{ model: string; requests: number; tokens: number }>;
}

export interface ApiKeyItem {
  id: string;
  name: string;
  prefix: string;
  createdAt: string;
  lastUsedAt?: string;
  revokedAt?: string;
}

export interface ApiKeysListResponse {
  items: ApiKeyItem[];
}

export interface ApiKeyCreateRequest {
  name: string;
}

export interface ApiKeyCreateResponse {
  item: ApiKeyItem;
  key: string;
}

export interface AnnouncementItem {
  id: string;
  title: string;
  meta: string;
  level: "info" | "warning" | "success" | "destructive" | string;
  createdAt: string;
}

export interface AnnouncementsListResponse {
  items: AnnouncementItem[];
}

export interface AnnouncementCreateRequest {
  title: string;
  meta: string;
  level: "info" | "warning" | "success" | "destructive";
}

export interface AnnouncementCreateResponse {
  item: AnnouncementItem;
}

export interface AnnouncementUpdateRequest {
  title: string;
  meta: string;
  level: "info" | "warning" | "success" | "destructive";
}

export interface AnnouncementUpdateResponse {
  item: AnnouncementItem;
}

export interface AnnouncementDeleteResponse {
  ok: boolean;
  id: string;
}

export interface AdminUserItem {
  id: string;
  email: string;
  role: "admin" | "user" | string;
  group: string;
  balance: number;
  bannedAt?: string | null;
  createdAt: string;
  lastLoginAt?: string | null;
  apiKeysTotal: number;
  apiKeysActive: number;
  sessionsActive: number;
}

export interface AdminUsersListResponse {
  items: AdminUserItem[];
}

export interface AdminUserUpdateRequest {
  balance?: number;
  banned?: boolean;
  group?: string | null;
  role?: "owner" | "admin" | "billing" | "developer" | "viewer" | string | null;
}

export interface AdminUserUpdateResponse {
  item: AdminUserItem;
}

export interface AdminUserDeleteResponse {
  ok: boolean;
  id: string;
}

export interface LlmChannelItem {
  id: string;
  name: string;
  baseUrl: string;
  apiKeyMasked: string;
  allowGroups: string[];
  createdAt: string;
  updatedAt: string;
}

export interface LlmChannelsListResponse {
  items: LlmChannelItem[];
}

export interface LlmChannelCreateRequest {
  name: string;
  baseUrl: string;
  apiKey: string;
  allowGroups: string[];
}

export interface LlmChannelCreateResponse {
  item: LlmChannelItem;
}

export interface LlmChannelUpdateRequest {
  name?: string;
  baseUrl?: string;
  apiKey?: string;
  allowGroups?: string[];
}

export interface LlmChannelUpdateResponse {
  item: LlmChannelItem;
}

export interface LlmChannelDeleteResponse {
  ok: boolean;
  id: string;
}

export interface AdminModelItem {
  model: string;
  enabled: boolean;
  inputUsdPerM?: string | null;
  outputUsdPerM?: string | null;
  sources: number;
  available: boolean;
}

export interface AdminModelsListResponse {
  items: AdminModelItem[];
}

export interface AdminModelUpdateRequest {
  enabled?: boolean;
  inputUsdPerM?: string | null;
  outputUsdPerM?: string | null;
}

export interface AdminModelUpdateResponse {
  item: AdminModelItem;
}

export interface ModelCatalogItem {
  model: string;
  inputUsdPerM?: string | null;
  outputUsdPerM?: string | null;
  sources: number;
}

export interface ModelsListResponse {
  items: ModelCatalogItem[];
}
