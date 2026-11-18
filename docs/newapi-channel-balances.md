# Site account balances in channel observation

Channel observation, channel details, balance management, and site-account
management share the same account wallet query. For New API this is backed by
the authenticated `/api/user/self` wallet. An account is queried once across
its groups and channels; the resulting balance is shared, not additive.

Imported channels use their recorded account/group association. Configured
channels use exact, fully verified bindings only; unmatched, partially matched
and ambiguous credentials never gain a wallet by hostname. A missing wallet
is not zero. A refresh failure marks any retained amount as the last result
and excludes it from the verified-balance filters.

API-key quotas, subscriptions and periodic limits are not displayed or
queried by the account console for balance. Standalone legacy connections
may still read the gateway's `/v1/channel-balances` for its historical
`actual_cost_usd` field when displaying a supported calendar range; other
fields from that response never enter the wallet display.

The existing account adapter divides raw `quota` by `quota_per_unit` to obtain
the console's USD accounting amount. It does not use the display-oriented
`/dashboard/billing/subscription` and `/dashboard/billing/usage` values: New API
can convert those values to CNY or token display units. Existing receipt totals
and their time/model scope remain independent of the wallet.

Upstream contracts: [user controller](https://github.com/QuantumNous/new-api/blob/2de51a19e35a602a0041d380b29be7c9bcd1e0e5/controller/user.go),
[billing controller](https://github.com/QuantumNous/new-api/blob/2de51a19e35a602a0041d380b29be7c9bcd1e0e5/controller/billing.go),
[billing routes](https://github.com/QuantumNous/new-api/blob/2de51a19e35a602a0041d380b29be7c9bcd1e0e5/router/dashboard.go).
Reading wallets does not send model requests or change gateway configuration.
