import { expect, it } from "vitest";
import { automaticChannelDocument } from "./channelProtocol";

it("delegates automatic inference without rewriting URLs, mixed models or aliases", () => {
  const document = {
    provider: "site",
    engine: "auto",
    base_url: "https://site.test/proxy/v1/responses?version=1",
    api: "fixture",
    model: ["gpt-6-luna", { "claude-fable-5": "gpt-alias" }, "unknown"],
  };
  expect(automaticChannelDocument(document)).toEqual({
    provider: "site",
    base_url: document.base_url,
    api: "fixture",
    model: document.model,
  });
  expect(document.engine).toBe("auto");
});

it("preserves explicit and omitted engines for special addresses", () => {
  for (const engine of ["gpt", "codex", "aws", undefined]) {
    const document = {
      provider: "site",
      engine,
      base_url: "https://bedrock.example/v1",
      model: ["claude-fable-5"],
    };
    expect(automaticChannelDocument(document)).toBe(document);
  }
});
