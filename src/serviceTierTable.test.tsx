import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { observedTierStatus, ServiceTierCell, tierIndex } from "./serviceTierTable";
import { serviceTiers } from "./serviceTierStatus";
import type { Channel } from "./types";
import type { InstalledChannel } from "./sub2apiImports";
import type { ConfiguredCheck } from "./channelManagement";

it.each(serviceTiers)(
  "%s uses the availability style for verified support only",
  (tier) => {
    const { rerender } = render(
      <ServiceTierCell
        tier={tier}
        index={new Map()}
        targets={[]}
        siteStatuses={["supported"]}
      />,
    );
    const supported = screen.getByText("已验证支持");
    expect(supported).toHaveClass("check-status", "pass");
    expect(supported.querySelector("svg")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
    rerender(
      <ServiceTierCell
        tier={tier}
        index={new Map()}
        targets={[]}
        siteStatuses={["not_honored"]}
      />,
    );
    expect(screen.getByText("未命中")).not.toHaveClass("pass");
  },
);

it("shows the imported group's Ultrafast result for the matching public and upstream model only", () => {
  const checked = Math.floor(Date.now()/1000);
  const scope = "a".repeat(64);
  const row = {source_id:"primary",provider:"ccodex-ultra",model:"public-alias",upstream_model:"gpt-6.2-sol"} as Channel;
  const member: InstalledChannel = {source_id:"primary", source_name:"Fugue", provider:row.provider, name:"ccodex",
    account_id:"ccodex", group_id:7, api_key_id:"", key_position:0, key_prefix:"", positions:{}, revision:"v1", manageable:true,
    engine:"codex", models:[row.model], model_mappings:{[row.model]:row.upstream_model}, service_tier_scope:scope,
    group_tier_results:{[row.model]:{ultrafast:{status:"supported",checked_at:checked-5}}}};
  expect(observedTierStatus("ultrafast", row, member, tierIndex([]))).toBe("supported");
  expect(observedTierStatus("fast", row, member, tierIndex([]))).toBeUndefined();
  expect(observedTierStatus("ultrafast", {...row,upstream_model:"gpt-different"}, member, tierIndex([]))).toBeUndefined();
  const exact: ConfiguredCheck = {source_id:member.source_id, provider:row.provider, model:row.model,
    state:"done", kind:"ultrafast", fingerprint:scope, message:"", history:undefined,
    result:{status:"not_honored",binding:scope,checked_at:checked}};
  expect(observedTierStatus("ultrafast", row, member, tierIndex([exact]))).toBeUndefined();
  expect(observedTierStatus("ultrafast", row, member, tierIndex([{...exact, result:{status:"inconclusive",checked_at:checked}}]))).toBe("supported");
});
