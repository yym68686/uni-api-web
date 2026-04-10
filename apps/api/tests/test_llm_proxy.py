from __future__ import annotations

import unittest

from app.api.llm_proxy import UsagePricing, estimate_cost_usd_micros


class EstimateCostUsdMicrosTests(unittest.TestCase):
    def test_counts_cached_input_at_ten_percent(self) -> None:
        pricing = UsagePricing(input_usd_micros_per_m=2_000_000, output_usd_micros_per_m=8_000_000)

        cost = estimate_cost_usd_micros(
            pricing=pricing,
            input_tokens=1_000,
            cached_tokens=300,
            output_tokens=400,
        )

        self.assertEqual(cost, 4_660)

    def test_clamps_cached_tokens_to_input_tokens(self) -> None:
        pricing = UsagePricing(input_usd_micros_per_m=1_000_000, output_usd_micros_per_m=None)

        cost = estimate_cost_usd_micros(
            pricing=pricing,
            input_tokens=100,
            cached_tokens=250,
            output_tokens=0,
        )

        self.assertEqual(cost, 10)

    def test_returns_zero_when_pricing_is_missing(self) -> None:
        pricing = UsagePricing(input_usd_micros_per_m=None, output_usd_micros_per_m=None)

        cost = estimate_cost_usd_micros(
            pricing=pricing,
            input_tokens=10_000,
            cached_tokens=5_000,
            output_tokens=10_000,
        )

        self.assertEqual(cost, 0)


if __name__ == "__main__":
    unittest.main()
