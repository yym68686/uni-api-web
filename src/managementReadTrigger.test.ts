import { expect, it } from "vitest";
import { createManagementReadTriggers, managementReadTrigger } from "./managementReadTrigger";

it("classifies each started read, with structural changes ahead of accepted writes", () => {
  const triggers = createManagementReadTriggers();
  expect(triggers.next("private-owner", "private-filters", "")).toBe("mount");
  expect(triggers.next("private-owner", "private-filters", "")).toBe("refetch");
  expect(triggers.next("private-owner", "changed-filters", "")).toBe("filter_change");
  expect(triggers.next("private-owner", "changed-filters", "private-cursor")).toBe("page_change");
  triggers.acceptedWrite("private-owner");
  expect(triggers.next("private-owner", "changed-filters", "private-cursor")).toBe("accepted_write");
  expect(triggers.next("private-owner", "changed-filters", "private-cursor")).toBe("refetch");
  triggers.acceptedWrite("private-owner");
  expect(triggers.next("private-owner", "new-filters", "")).toBe("filter_change");
  expect(triggers.next("private-owner", "new-filters", "")).toBe("refetch");
  triggers.acceptedWrite("private-owner");
  expect(triggers.next("other-private-owner", "new-filters", "")).toBe("mount");
  expect(triggers.next("other-private-owner", "new-filters", "")).toBe("refetch");
  triggers.acceptedWrite("private-owner");
  expect(triggers.next("other-private-owner", "new-filters", "")).toBe("refetch");
  const otherHook = createManagementReadTriggers();
  expect(otherHook.next("other-private-owner", "new-filters", "")).toBe("mount");
  expect(JSON.stringify(triggers)).toBe("{}");
});

it("admits only fixed labels, never coercing or exposing private header values", () => {
  for (const value of ["mount", "filter_change", "page_change", "accepted_write", "refetch"])
    expect(managementReadTrigger(value)).toBe(value);
  for (const value of ["unspecified", null, undefined, 3, {}, [], "private-owner", " mount", "page_change,refetch", "private\nforged", { toString: () => { throw Error("must not coerce"); } }])
    expect(managementReadTrigger(value)).toBe("unspecified");
});
