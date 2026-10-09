export const channelProtocols = [
  {
    protocol: "responses",
    engine: "codex",
    endpoint: "/v1/responses",
    label: "Responses / Codex",
  },
  {
    protocol: "messages",
    engine: "claude",
    endpoint: "/v1/messages",
    label: "Claude Messages",
  },
  {
    protocol: "gemini",
    engine: "gemini",
    endpoint: "/v1beta",
    label: "Gemini",
  },
  {
    protocol: "chat",
    engine: "gpt",
    endpoint: "/v1/chat/completions",
    label: "Chat Completions",
  },
] as const;
export function automaticChannelDocument(document: Record<string, unknown>) {
  if (document.engine !== "auto") return document;
  const result = { ...document };
  delete result.engine;
  return result;
}

export function channelEngineLabel(channel?: {
  engine?: string;
  engine_mode?: string;
}) {
  if (channel?.engine_mode === "auto")
    return channel.engine ? `自动识别（实际：${channel.engine}）` : "自动识别";
  if (channel?.engine_mode === "explicit")
    return channel.engine ? `手动指定：${channel.engine}` : "手动指定";
  return channel?.engine ? `实际引擎：${channel.engine}（配置模式未知）` : "";
}
