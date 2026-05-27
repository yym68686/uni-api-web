from __future__ import annotations

import unittest
import uuid

from app.models.llm_model_pricing_rule import LlmModelPricingRule
from app.storage.models_db import model_pricing_rule_to_item, price_detail_for_model_from_rules


class ModelPricingRuleTests(unittest.TestCase):
    def test_longest_prefix_rule_wins_and_applies_discount(self) -> None:
        org_id = uuid.uuid4()
        rules = [
            LlmModelPricingRule(
                org_id=org_id,
                prefix="gpt-5",
                input_usd_micros_per_m_original=3_000_000,
                output_usd_micros_per_m_original=30_000_000,
                discount=0.5,
            ),
            LlmModelPricingRule(
                org_id=org_id,
                prefix="gpt-5.4",
                input_usd_micros_per_m_original=2_500_000,
                output_usd_micros_per_m_original=15_000_000,
                discount=0.01,
            ),
        ]

        detail = price_detail_for_model_from_rules("gpt-5.4-mini-2026-05", rules)

        self.assertEqual(detail, (25_000, 150_000, 2_500_000, 15_000_000, 0.01))

    def test_pricing_rule_item_exposes_original_and_effective_price(self) -> None:
        rule = LlmModelPricingRule(
            org_id=uuid.uuid4(),
            prefix="gemini-2.5-flash",
            input_usd_micros_per_m_original=600_000,
            output_usd_micros_per_m_original=5_000_000,
            discount=0.15,
        )

        item = model_pricing_rule_to_item(rule)

        self.assertEqual(item["inputUsdPerMOriginal"], "0.6")
        self.assertEqual(item["outputUsdPerMOriginal"], "5")
        self.assertEqual(item["inputUsdPerM"], "0.09")
        self.assertEqual(item["outputUsdPerM"], "0.75")
        self.assertEqual(item["discount"], 0.15)


if __name__ == "__main__":
    unittest.main()
