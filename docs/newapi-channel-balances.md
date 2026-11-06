# New API channel wallet fallback

The observation table normally reads the gateway's `/v1/channel-balances`.
Its Sub2API adapter calls `/v1/usage`, which New API sites can return 404 for.
The site-account list instead uses the authenticated `/api/user/self` wallet.

For an imported channel with a verified account/group association, a complete
404/405 missing-usage result may use that same account wallet when the account
adapter is `newapi`. The balance response includes `provider_kind`, including
legacy cached balances; management also seeds this metadata into the shared
account query. One account is queried once across its groups and channels.
The display explicitly identifies this value as the site account balance.

Successful key quotas, subscriptions and periodic limits remain unchanged.
Authentication, rate-limit, network and timeout errors do not qualify for this
fallback. Neither incomplete key results nor omitted keys qualify. Unmatched,
partially matched and ambiguous credentials never gain a wallet by hostname.
An unavailable wallet remains missing/error; it is not turned into zero.

The existing account adapter divides raw `quota` by `quota_per_unit` to obtain
the console's USD accounting amount. It does not use the display-oriented
`/dashboard/billing/subscription` and `/dashboard/billing/usage` values: New API
can convert those values to CNY or token display units. Existing receipt totals
and their time/model scope remain independent of the wallet fallback.

Upstream contracts: [user controller](https://github.com/QuantumNous/new-api/blob/2de51a19e35a602a0041d380b29be7c9bcd1e0e5/controller/user.go),
[billing controller](https://github.com/QuantumNous/new-api/blob/2de51a19e35a602a0041d380b29be7c9bcd1e0e5/controller/billing.go),
[billing routes](https://github.com/QuantumNous/new-api/blob/2de51a19e35a602a0041d380b29be7c9bcd1e0e5/router/dashboard.go).
No model requests, gateway configuration changes, token creation or credential
retransmission are needed for this fallback.
