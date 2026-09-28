# Console loading on slow connections

The management page requests an authenticated account header separately from
model results, so the account area can render while models arrive. Results use
`X-Console-View: summary`: all rows, prices, filtering and eligibility fields stay
available, while original replies, detailed receipts and probe attempts are read
only by the account/group/model diagnostic dialog. The default full API remains
available during rolling upgrades.

Clients send the last `X-Console-Since` version. Unchanged data returns 304;
changed data returns replacement rows, deletions and a complete order. Cursors
are scoped by authenticated user, endpoint, view and query. A bounded ten-minute
hash history falls back to a full summary on restart/expiry. No diagnostic bodies
are retained in this history. Frontend merges reject missing rows or a mismatched
base. Idle result polling is once a minute; active checks poll every five seconds.

Private account summaries may be restored from IndexedDB after authentication.
They are keyed by origin and username, expire after 30 minutes, and are cleared
on logout. Cached data is marked until a network response confirms its version.
Cache access is optional and bounded; live reads still work if storage fails.
Writes retain their existing server revision checks and are never automatically
replayed. Read cancellation and a 30-second deadline are combined; writes have a
120-second ceiling matching the proxy's mutation budget.

Inactive observation queries are paused on the management page. Settings,
model editors, management, prices and automation code are split into chunks.
System fonts avoid separate font requests. A load failure offers an explicit
reload action instead of a blank root; users are warned that reload discards
unsaved edits. Nginx compression applies to clients with a Via header as well.

## Validation (2026-09-28)

- Frontend: 303 tests, type check and production build; nginx upstream tests.
- Backend: complete Go suite with an isolated PostgreSQL database, including
  summary field preservation, delta, deletion, unchanged responses, owner
  isolation and scoped full diagnostics.
- Local production build, nginx and read-only synthetic HTTP fixture: 26
  accounts, 260 groups, 6,760 model results. Raw original list 11,094,868 bytes;
  compact JSON 2,338,719 bytes; actual nginx wire body approximately 77 KB with
  gzip and Via. Unchanged response has zero body bytes.
- Browser whole-tab throttling, RTT 300 ms: at 16 KB/s, cold account display
  19.9 s and full channel summaries 27.1 s; at 27 KB/s, 12.5 s and 16.8 s.
  A subsequent cached visit at 16 KB/s displayed accounts at 1.2 s and channel
  rows at 1.7 s, including authentication. These are synthetic local results,
  not production guarantees; cold results include downloading all required JS.

This release reduces transfer size and repeated traffic. It does not claim to
resolve the previously observed 176-second public-connection TTFB, eliminate
TCP retransmits, or meet the initial design's 80 KB cold-JS budget. It continues
to reconstruct all summaries before global filtering, so filters never silently
operate on a partial page. Database summary reads still load the existing result
objects before compaction; SQL projection/pagination and further startup code
splitting remain potential follow-up optimizations.
