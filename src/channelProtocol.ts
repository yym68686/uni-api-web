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
