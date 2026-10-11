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
export function protocolForEngine(engine?: string) {
  return channelProtocols.find((p) => p.engine === engine)?.protocol || "";
}
export function channelProtocolLabel(protocol?: string) {
  const spec = channelProtocols.find((p) => p.protocol === protocol);
  return spec ? `${spec.label} · ${spec.engine}` : "协议未确认";
}
export function automaticChannelDocument(document: Record<string, unknown>) {
  if (document.engine !== "auto") return document;
  const result = { ...document };
  delete result.engine;
  return result;
}

export function channelEngineLabel(channel?: {
  engine?: string;
  engine_mode?: string;
  engine_origin?: string;
}) {
  if (channel?.engine_mode === "auto")
    return channel.engine ? `自动识别（实际：${channel.engine}）` : "自动识别";
  if (channel?.engine_origin === "legacy_default")
    return `旧版默认：${channel.engine || "gpt"}`;
  if (channel?.engine_origin === "user_override")
    return `人工覆盖：${channel.engine || "未确认"}`;
  if (channel?.engine_mode === "explicit")
    return `显式配置：${channel.engine || "未确认"}（来源未确认）`;
  return channel?.engine ? `实际引擎：${channel.engine}（配置模式未知）` : "";
}
