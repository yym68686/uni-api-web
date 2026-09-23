import { expect, it } from "vitest";
import { splitChannelModels } from "./ChannelModelSelection";
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
