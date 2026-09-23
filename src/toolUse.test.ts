import { expect, it } from "vitest";
import {
  modelToolUse,
  toolUseStatus,
  toolUseFailed,
  toolUseMatches,
} from "./toolUse";
import type { SubTarget } from "./Sub2apiChecks";
import { configuredToolUseResult } from "./channelManagement";
import type { ManagedChannel, ConfiguredCheck } from "./channelManagement";

const target = (overrides: Partial<SubTarget> = {}) =>
  ({
    models: ["gpt-5.5", "gpt-6-sol"].map((model) => ({
      model,
      state: "done",
      message: "",
      result: { model, availability: { status: "success" } },
    })),
    ...overrides,
  }) as SubTarget;
it("legacy exec success applies only to the actual tested model", () => {
  const t = target({
    tool_use: {
      status: "supported",
      model: "gpt-5.5",
      checked_at: 1,
      attempts: [],
    },
  });
  expect(toolUseStatus(t, "gpt-5.5")).toBe("supported");
  expect(toolUseStatus(t, "gpt-6-sol")).toBe("untested");
  expect(toolUseStatus(t)).toBe("untested");
  expect(toolUseFailed(t, "gpt-6-sol")).toBe(false);
});
it("preserves independent pass, unsupported, transport failure and untested states", () => {
  const t = target({
    tool_use: {
      status: "unsupported",
      checked_at: 1,
      attempts: [],
      models: [
        {
          model: "gpt-5.5",
          state: "done",
          result: { status: "supported", checked_at: 1, attempts: [] },
        },
        {
          model: "gpt-6-sol",
          state: "done",
          result: { status: "unsupported", checked_at: 1, attempts: [] },
        },
        {
          model: "gpt-6-astra",
          state: "done",
          result: { status: "error", checked_at: 1, attempts: [] },
        },
        { model: "gpt-6-luna", state: "queued" },
      ],
    },
  });
  expect(toolUseFailed(t, "gpt-6-sol")).toBe(true);
  expect(toolUseFailed(t, "gpt-6-astra")).toBe(true);
  expect(toolUseFailed(t, "gpt-5.5")).toBe(false);
  expect(toolUseFailed(t, "gpt-6-luna")).toBe(false);
  expect(toolUseMatches(t, "supported", "gpt-6-sol")).toBe(false);
  expect(toolUseMatches(t, "supported", "gpt-5.5")).toBe(true);
  expect(toolUseMatches(t, "error")).toBe(true);
  expect(toolUseMatches(t, "untested")).toBe(true);
});
it("native import reads only the selected source and current credentials", () => {
  const member = {
    source_id: "a",
    provider: "native",
    models: ["gpt-6-sol"],
    probe_fingerprint: "current",
  } as ManagedChannel;
  const checks = [
    {
      source_id: "a",
      provider: "native",
      kind: "tool-use",
      model: "gpt-6-sol",
      fingerprint: "current",
      state: "done",
      message: "",
      history: {total:0,successful:0,passed:0},
      result: {
        status: "unsupported",
        model: "gpt-6-sol",
        checked_at: 1,
        attempts: [],
      },
    },
    {
      source_id: "b",
      provider: "native",
      kind: "tool-use",
      model: "gpt-6-sol",
      fingerprint: "current",
      state: "done",
      message: "",
      history: {total:0,successful:0,passed:0},
      result: {
        status: "supported",
        model: "gpt-6-sol",
        checked_at: 2,
        attempts: [],
      },
    },
  ] as ConfiguredCheck[];
  expect(
    modelToolUse(
      { tool_use: configuredToolUseResult(member, [], checks) },
      "gpt-6-sol",
    )?.status,
  ).toBe("unsupported");
  expect(
    configuredToolUseResult(
      { ...member, probe_fingerprint: "changed" },
      [],
      checks,
    ).models,
  ).toHaveLength(0);
});
