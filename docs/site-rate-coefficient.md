# Site account rate coefficient

Each site account has a persisted `rate_coefficient`, defaulting to `1`.
The account settings dialog accepts finite values greater than zero and at most
1,000,000. `PATCH /v1/sub2api/accounts/{id}/settings` is owner scoped and changes
only this field, including while a sync job is running.

Management billing-rate cells, filters, sort order and generated channel labels
use the account coefficient. Upstream advertised and observed rates remain raw
in storage and API evidence; targets carry the coefficient separately. Probe
details convert the displayed paid prices, rate and actual cost, while reference
and pre-multiplier unit prices retain their original meaning.

Channel spend applies the coefficient to each exactly attributed receipt before
summing. A provider that used two accounts in the selected window uses each
account's own coefficient. Complete, partial and history-limited amounts share
this conversion, preserving all existing unknown/ambiguity semantics. Profit
uses the converted amount once. Whole business-key totals also convert at read
time. Upstream receipts, wallet balances and estimated customer charges stay raw.

The current coefficient also applies to historical display; changing it does
not rewrite the receipt ledger, rerun probes, or change gateway configuration.
The migration adds a defaulted column and extends the account view revision
trigger so existing management pages, cursors and server caches are invalidated
when the value changes. Identical saves preserve that revision. The frontend
invalidates management, billing and observation queries after a successful save.
