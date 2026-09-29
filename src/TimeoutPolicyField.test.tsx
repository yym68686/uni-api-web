import { useState } from "react";
import { expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TimeoutPolicyField, HedgingField } from "./TimeoutPolicyField";
it("edits endpoint and stream rules while preserving advanced conditions and explicit zero", async () => {
  let current: unknown;
  function Fixture() {
    const [value, setValue] = useState<unknown>({
      default: { connect: 5 },
      rules: [
        {
          match: { endpoint: "/v1/responses", stream: false, engine: "gpt" },
          timeout: { total: 180 },
        },
      ],
    });
    current = value;
    return <TimeoutPolicyField value={value} onChange={setValue} />;
  }
  render(<Fixture />);
  const user = userEvent.setup();
  expect(screen.getByLabelText("规则 1 流式状态")).toHaveValue("false");
  await user.selectOptions(screen.getByLabelText("规则 1 流式状态"), "true");
  await user.clear(screen.getByLabelText("规则 1 首字超时（秒）"));
  await user.type(screen.getByLabelText("规则 1 首字超时（秒）"), "0");
  expect(current).toEqual({
    default: { connect: 5 },
    rules: [
      {
        match: { endpoint: "/v1/responses", stream: true, engine: "gpt" },
        timeout: { total: 180, first_byte: 0 },
      },
    ],
  });
  await user.click(screen.getByRole("button", { name: "添加超时规则" }));
  await user.selectOptions(screen.getByLabelText("规则 2 流式状态"), "false");
  await user.click(screen.getByRole("button", { name: "上移超时规则 2" }));
  expect((current as any).rules[0].match.stream).toBe(false);
  expect((current as any).rules[1].match.engine).toBe("gpt");
});
it("offers the actual hedging toggle and concurrency without changing the winner policy", async () => {
  let current: unknown;
  function Fixture() {
    const [value, setValue] = useState<unknown>({
      enabled: false,
      max_inflight_attempts: 2,
      winner_policy: "first_valid_success",
    });
    current = value;
    return <HedgingField value={value} onChange={setValue} />;
  }
  render(<Fixture />);
  await userEvent
    .setup()
    .click(screen.getByRole("checkbox", { name: "启用 hedging" }));
  expect(current).toEqual({
    enabled: true,
    max_inflight_attempts: 2,
    winner_policy: "first_valid_success",
  });
});
