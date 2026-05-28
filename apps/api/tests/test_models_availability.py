from __future__ import annotations

import unittest

from sqlalchemy.dialects import postgresql

from app.storage.models_db import (
    _availability_legacy_slots,
    _model_availability_relevant_filter,
    model_availability_bucket,
)


class ModelAvailabilityTests(unittest.TestCase):
    def test_low_sample_bucket_is_unknown_even_with_failure(self) -> None:
        bucket = model_availability_bucket(total=1, failed=1)

        self.assertEqual(bucket["total"], 1)
        self.assertEqual(bucket["failed"], 1)
        self.assertEqual(bucket["status"], "unknown")

    def test_bucket_status_uses_failure_rate_thresholds(self) -> None:
        self.assertEqual(model_availability_bucket(total=20, failed=0)["status"], "healthy")
        self.assertEqual(model_availability_bucket(total=20, failed=1)["status"], "degraded")
        self.assertEqual(model_availability_bucket(total=10, failed=2)["status"], "down")

    def test_legacy_slots_only_mark_down_buckets(self) -> None:
        slots = _availability_legacy_slots(
            [
                model_availability_bucket(total=20, failed=0),
                model_availability_bucket(total=20, failed=1),
                model_availability_bucket(total=10, failed=2),
                model_availability_bucket(total=1, failed=1),
            ]
        )

        self.assertEqual(slots, [0, 0, 1, 0])

    def test_relevant_filter_excludes_client_statuses(self) -> None:
        sql = str(
            _model_availability_relevant_filter().compile(
                dialect=postgresql.dialect(),
                compile_kwargs={"literal_binds": True},
            )
        )

        self.assertIn("llm_usage_events.status_code < 400", sql)
        self.assertIn("llm_usage_events.status_code >= 500", sql)
        self.assertNotIn("401", sql)
        self.assertNotIn("403", sql)
        self.assertNotIn("408", sql)
        self.assertNotIn("429", sql)
        self.assertNotIn("499", sql)


if __name__ == "__main__":
    unittest.main()
