import { expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { ModelQualityWarning } from "./ModelQualityWarning";

it("marks only confirmed Astra degradation, not failed or missing detection", () => {
  const view = render(
    <ModelQualityWarning
      model="gpt-6-astra"
      result={{ verdict: "fail", checked_at: 100 }}
    />,
  );
  expect(screen.getByText("降智")).toHaveClass("model-quality-warning");
  expect(screen.getByText("降智").title).toContain("可用性与降智分别判断");
  for (const verdict of [
    "pass",
    "error",
    "inconclusive",
    "not_applicable",
    "",
  ]) {
    view.rerender(
      <ModelQualityWarning
        model="gpt-6-astra"
        result={{ verdict, checked_at: 101 }}
      />,
    );
    expect(screen.queryByText("降智")).not.toBeInTheDocument();
  }
  view.rerender(
    <ModelQualityWarning
      model="gpt-6-sol"
      result={{ verdict: "fail", checked_at: 100 }}
    />,
  );
  expect(screen.queryByText("降智")).not.toBeInTheDocument();
  view.rerender(<ModelQualityWarning model="gpt-6-astra" />);
  expect(screen.queryByText("降智")).not.toBeInTheDocument();
});
