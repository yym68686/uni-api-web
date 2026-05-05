from __future__ import annotations

import unittest

from app.storage.balance_math import remaining_usd_2_from_micros


class BalanceMathTests(unittest.TestCase):
    def test_remaining_usd_2_from_micros_subtracts_spend_from_credits(self) -> None:
        self.assertEqual(
            remaining_usd_2_from_micros(
                credits_usd_micros=25_000_000,
                spend_usd_micros_total=14_320_000,
            ),
            10.68,
        )

    def test_remaining_usd_2_from_micros_never_goes_negative(self) -> None:
        self.assertEqual(
            remaining_usd_2_from_micros(
                credits_usd_micros=5_000_000,
                spend_usd_micros_total=7_000_000,
            ),
            0.0,
        )


if __name__ == "__main__":
    unittest.main()
