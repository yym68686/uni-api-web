# Site DNS fallback

## Observed incident: jiji.cc (2026-10-14)

The site's public settings endpoint returned the standard sub2api `code: 0`
envelope. `/api/status` returned 404, which already selects the sub2api adapter.
The account-add error occurred before credentials were sent.

Read-only checks from the analytics pod on `ovhuswestagent` reproduced temporary
DNS failure (`EAI_AGAIN`) for `www.jiji.cc`. The node's upstream resolver
`213.186.33.99` timed out on A and AAAA queries for its CNAME chain, while public
resolvers returned addresses. With the same hostname, TLS validation and ordinary
Go HTTP user agent, using the resolved public address allowed login, identity
verification, and reading 17 groups through the existing sub2api APIs. A local
Python client separately encountered Cloudflare 1010; this was not the production
failure, and no browser impersonation or challenge bypass was introduced.

## Behavior

Site HTTP connections first use system DNS with a three-second budget. Only a
temporary DNS failure or timeout tries `1.1.1.1:53`, then `8.8.8.8:53`, each with
a two-second budget. The overall request context remains authoritative.

NXDOMAIN, empty answers, permanent failures and private-address answers do not
trigger fallback. Every address from every resolver is validated with the existing
public-address policy before dialing. Public fallback queries use the absolute
site name without cluster search suffixes; single-label names are not sent to
public resolvers. Connections still validate TLS against the original hostname,
and redirects remain disabled. There is no persistent address cache, IP pin or
cluster-wide DNS configuration change.

Successful recovery logs `event=site_dns_fallback`, host and resolver only. When
all eligible DNS attempts fail, account creation reports a DNS-specific error
instead of claiming the site type is unrecognized. No passwords, tokens, response
bodies or query strings are logged.

## Validation

The exact updated Go transport and resolver code was compiled as an isolated
diagnostic and run in the serving analytics pod without replacing its process.
The system resolver timed out; public fallback logged recovery after three
seconds. `/api/status` returned 404; login, `/api/v1/auth/me`,
`/api/v1/groups/available` and the existing key list returned 200, all within
4.1 seconds. The diagnostic executable was removed afterward. No keys were
created, production account settings changed, synchronization jobs started or
paid model calls made.

Regression tests cover transient recovery, absolute names, private/mixed answers,
NXDOMAIN, permanent errors, bounded waits, cancellation, working system DNS and
IP literals. Existing account and protocol suites exercise the unchanged adapter.
