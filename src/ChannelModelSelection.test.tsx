import { expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { ChannelModelSelection, splitChannelModels, unavailableModelChanges } from "./ChannelModelSelection";
it("classifies saved identity models as checked originals even with no model inventory", () => {
  expect(
    splitChannelModels([
      { model: "gpt-5.4", upstream_model: "gpt-5.5" },
      { model: "gpt-5.5", upstream_model: "gpt-5.5" },
      { model: "gpt-6-sol", upstream_model: "gpt-6-sol" },
    ]),
  ).toEqual({
    originals: ["gpt-5.5", "gpt-6-sol"],
    aliases: [{ public: "gpt-5.4", upstream: "gpt-5.5" }],
  });
});

it("permits removal of a saved failed model without allowing it to be added back", async () => {
  function Editor() {
    const [selected,setSelected]=useState(["saved-failed"]);
    return <ChannelModelSelection models={["saved-failed","new-failed","passed"]} selected={selected} onChange={setSelected} canSelect={m=>m==="passed"} editing/>;
  }
  render(<Editor/>);
  const user=userEvent.setup();
  const saved=screen.getByRole("checkbox",{name:"saved-failed"});
  expect(saved).toBeChecked();
  expect(saved).toBeEnabled();
  expect(screen.getByRole("checkbox",{name:"new-failed"})).toBeDisabled();
  await user.click(saved);
  expect(saved).not.toBeChecked();
  expect(saved).toBeDisabled();
  await user.click(screen.getByRole("checkbox",{name:"passed"}));
  expect(screen.getByRole("checkbox",{name:"passed"})).toBeChecked();
});

it("preserves exact saved pairs but requires passing evidence for new aliases and changed upstreams",()=>{
  expect(unavailableModelChanges(
    {saved:"failed",alias:"failed",changed:"failed",good:"passed"},
    {saved:"failed",changed:"previous"},m=>m==="passed",
  )).toEqual(["alias","changed"]);
});
it("preserves configured public model semantics and separates independent aliases", () => {
  expect(
    splitChannelModels(
      [
        { model: "short", upstream_model: "actual" },
        { model: "another", upstream_model: "actual" },
        { model: "custom", upstream_model: "custom" },
      ],
      { models: ["short"], model_mappings: { short: "actual" } },
    ),
  ).toEqual({
    originals: ["short", "custom"],
    aliases: [{ public: "another", upstream: "short" }],
  });
});
