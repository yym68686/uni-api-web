import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { useState } from "react";
import { ConsoleLocator } from "./ConsoleChrome";
import { emptyStats } from "./analytics";
import type { Channel } from "./types";

it("locates results by keyboard without showing duplicate channels or unauthorized pages", async () => {
  const channel = { provider:"fixture-provider", model:"fixture-model", eligible:true, stats:emptyStats() } as Channel;
  const onPage = vi.fn(), onChannel = vi.fn(), onRequest = vi.fn();
  function Harness() {
    const [open, setOpen] = useState(true);
    return <><button onClick={() => setOpen(true)}>重新定位</button>
      <ConsoleLocator open={open} onOpenChange={setOpen} account={false}
        channels={[channel, {...channel}]} loading={false} failed={false}
        onPage={onPage} onChannel={onChannel} onRequest={onRequest} />
    </>;
  }
  render(<Harness />);
  const user = userEvent.setup();
  const search = screen.getByRole("searchbox");
  expect(search).toHaveFocus();
  expect(screen.queryByRole("button", {name:"渠道管理"})).not.toBeInTheDocument();
  await user.type(search, "fixture-provider");
  expect(screen.getAllByRole("button", {name:/^fixture-provider/})).toHaveLength(1);
  await user.keyboard("{Enter}");
  expect(onChannel).toHaveBeenCalledWith(channel);
  expect(onRequest).not.toHaveBeenCalled();
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", {name:"重新定位"}));
  expect(screen.getByRole("searchbox")).toHaveValue("");
  await user.type(screen.getByRole("searchbox"), "req-ab12");
  await user.keyboard("{ArrowDown}{Enter}");
  expect(onRequest).toHaveBeenCalledWith("req-ab12");
  expect(onPage).not.toHaveBeenCalled();
});
