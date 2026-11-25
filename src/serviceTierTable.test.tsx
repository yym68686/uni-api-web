import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ServiceTierCell } from "./serviceTierTable";
import { serviceTiers } from "./serviceTierStatus";

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
