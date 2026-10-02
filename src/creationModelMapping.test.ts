import { expect, it } from "vitest";
import {
  readCreationModels,
  writeCreationModels,
} from "./creationModelMapping";
it("round-trips one upstream under multiple public names and rejects colliding names", () => {
  const document = [
    "model-a",
    { "vendor/model-b": "public-b" },
    { "vendor/model-b": "public-c" },
  ];
  const { originals, aliases } = readCreationModels(document);
  expect(writeCreationModels(originals, aliases)).toEqual(document);
  expect(() => writeCreationModels(["public-b"], aliases)).toThrow("重复");
  expect(() => readCreationModels([{ "vendor/model-b": 42 }])).toThrow("文字");
});
