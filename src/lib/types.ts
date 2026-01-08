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
