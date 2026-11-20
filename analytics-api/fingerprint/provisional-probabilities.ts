/** Uncalibrated relative weights, not the three-answer calibration. */
export function provisionalProbabilities(scores: readonly (number | undefined)[]): number[] | null {
  if (scores.length < 2 || !scores.every(score => typeof score === "number" && Number.isFinite(score))) return null;
  const values = scores as readonly number[];
  const max = Math.max(...values);
  const weights = values.map(score => Math.exp(score - max));
  const total = weights.reduce((sum, weight) => sum + weight, 0);
  return weights.map(weight => weight / total);
}
