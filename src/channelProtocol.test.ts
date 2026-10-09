import { expect, it } from "vitest";
import {
  automaticChannelDocument,
  channelEngineLabel,
} from "./channelProtocol";

it("distinguishes automatic configuration from effective and explicit engines", () => {
  expect(channelEngineLabel({ engine: "codex", engine_mode: "auto" })).toBe(
    "自动识别（实际：codex）",
  );
  expect(channelEngineLabel({ engine: "claude", engine_mode: "auto" })).toBe(
    "自动识别（实际：claude）",
  );
  expect(channelEngineLabel({ engine: "gpt", engine_mode: "explicit" })).toBe(
    "手动指定：gpt",
  );
  expect(channelEngineLabel({ engine: "codex" })).toBe(
    "实际引擎：codex（配置模式未知）",
  );
  expect(channelEngineLabel()).toBe("");
});

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
