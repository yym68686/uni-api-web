import { expect, it } from "vitest";
import {
  removesImageGeneration,
  setImageGenerationRemoval,
} from "./imageGenerationRemoval";

it("defaults off and generates the original YAML removal rules idempotently", () => {
  expect(removesImageGeneration(undefined)).toBe(false);
  expect(removesImageGeneration({})).toBe(false);
  const enabled = setImageGenerationRemoval(undefined, true);
  expect(enabled).toEqual({
    __remove__: [
      { path: "tools", where: { type: "image_generation" }, drop_empty: true },
      {
        path: "tool_choice",
        where_any: [
          "image_generation",
          { type: "image_generation" },
          { "tool.type": "image_generation" },
          { "function.name": "image_generation" },
        ],
      },
    ],
  });
  expect(removesImageGeneration(enabled)).toBe(true);
  expect(setImageGenerationRemoval(enabled, true)).toEqual(enabled);
  expect(setImageGenerationRemoval(enabled, false)).toBeUndefined();
});

it("preserves unrelated overrides and string or structured removal rules", () => {
  for (const remove of ["temperature", { path: "metadata" }, ["temperature"]]) {
    const original = {
      temperature: 0.2,
      "model-a": { max_output_tokens: 42 },
      __remove__: remove,
    };
    const snapshot = structuredClone(original);
    const enabled = setImageGenerationRemoval(original, true);
    expect(removesImageGeneration(enabled)).toBe(true);
    expect(setImageGenerationRemoval(enabled, false)).toEqual({
      ...original,
      __remove__: Array.isArray(remove) ? remove : [remove],
    });
    expect(original).toEqual(snapshot);
  }
});

it("recognizes reordered existing rules and only removes the exact preset", () => {
  const enabled = setImageGenerationRemoval({}, true);
  const remove = enabled!.__remove__ as Record<string, unknown>[];
  (remove[1].where_any as unknown[]).reverse();
  remove.reverse();
  const custom = {
    path: "tools",
    where: { type: "function" },
    drop_empty: true,
  };
  remove.push(custom);
  expect(removesImageGeneration(enabled)).toBe(true);
  expect(setImageGenerationRemoval(enabled, false)).toEqual({
    __remove__: [custom],
  });
  expect(removesImageGeneration({ __remove__: remove[0] })).toBe(false);
});
