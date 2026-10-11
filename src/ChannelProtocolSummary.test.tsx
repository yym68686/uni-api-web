import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ChannelProtocolSummary } from "./ChannelProtocolSummary";
import { optimizationFixture } from "./channelOptimization.test-data";

it("distinguishes actual configured engine from the model probe protocol in the channel list", () => {
  const s = optimizationFixture();
  s.imports.data[0].engine = "gpt";
  s.imports.data[0].engine_mode = "explicit";
  render(<ChannelProtocolSummary row={s.rows[0]} imports={s.imports} />);
  expect(screen.getByText("手动指定：gpt")).toBeInTheDocument();
  expect(
    screen.getByText("检测：Responses / Codex · codex"),
  ).toBeInTheDocument();
});
