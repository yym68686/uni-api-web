from __future__ import annotations

import unittest

from sqlalchemy.dialects import postgresql

from app.storage import admin_analytics_db
from app.storage.billing_db import ledger_spend_micros_at_entry_expr


class UsageRollupQueryTests(unittest.TestCase):
    def test_admin_analytics_main_stats_use_hourly_rollups(self) -> None:
        sql = str(admin_analytics_db._ADMIN_ANALYTICS_SQL)

        self.assertIn("stats_filtered AS MATERIALIZED", sql)
        self.assertIn("FROM llm_usage_hourly_stats", sql)
        self.assertIn("COALESCE(SUM(requests)", sql)
        self.assertIn("raw_latency_kpi", sql)
        self.assertIn("raw_filtered AS MATERIALIZED", sql)

    def test_billing_ledger_spend_uses_hourly_rollups(self) -> None:
        sql = str(
            ledger_spend_micros_at_entry_expr().compile(dialect=postgresql.dialect())
        )

        self.assertIn("llm_usage_hourly_stats", sql)
        self.assertNotIn("llm_usage_events", sql)


if __name__ == "__main__":
    unittest.main()
