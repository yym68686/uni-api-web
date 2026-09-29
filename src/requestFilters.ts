import { ApiError, request } from "./api";
import type { Connection, KeyInfo } from "./types";

export interface Keys {
  data: KeyInfo[];
  snapshot_revision: string;
  can_inspect_all: boolean;
}

export const endpointChoices = [
  "/v1/systemone",
  "/v1/responses",
  "/v1/responses/compact",
  "/v1/chat/completions",
  "/v1/messages",
  "/v1/embeddings",
  "/v1/images/generations",
  "/v1/images/edits",
  "/v1/audio/speech",
  "/v1/audio/transcriptions",
  "/v1/audio/translations",
  "/v1/moderations",
];

export async function readKeys(connection: Connection, signal: AbortSignal) {
  const keys = await request<Keys>(connection, "/v1/api-keys", signal);
  if (!Array.isArray(keys.data))
    throw new Error("服务未提供平台目录，请检查 uni-api 版本与权限。");
  if (!keys.can_inspect_all)
    throw new ApiError(
      "密钥没有平台查看权限，请使用配置中的第一个密钥或管理员密钥。",
      403,
    );
  return keys;
}
