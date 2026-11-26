import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ChannelTierTPS } from "./channelTierTPS";

it("shows separate p50 and p95 TPS and keeps the same second line in every state", () => {
  const {rerender} = render(<ChannelTierTPS loading={true} failed={false} />);
  const line = screen.getByText("读取中…").parentElement;
  rerender(<ChannelTierTPS value={{p50:12.34,p95:45.67,samples:15}} loading={false} failed={false} />);
  expect(screen.getByText("p50 12.3").parentElement).toBe(line);
  expect(screen.getByText("p95 45.7").parentElement).toBe(line);
  expect(screen.getByText("TPS")).toBeVisible();
  rerender(<ChannelTierTPS loading={false} failed={true} />);
  expect(screen.getByText("读取失败").parentElement).toBe(line);
  rerender(<ChannelTierTPS loading={false} failed={false} />);
  expect(screen.getByText("—").parentElement).toBe(line);
});
