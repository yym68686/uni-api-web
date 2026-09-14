# uni-api console

A new React + TypeScript workspace for observing uni-api model channels. This
repository's previous frontend has been replaced; historical versions remain in
Git history.

## Run locally

```sh
npm ci
npm run dev
```

Open http://127.0.0.1:4173 and enter your uni-api public endpoint and the first
configured key (or an admin key). The backend must provide the platform APIs
listed below and allow the browser origin through CORS. HTTPS deployments require
an HTTPS backend endpoint. Verified connections are saved in the current tab's
`sessionStorage`, allowing reloads to restore the connection without another login.
The platform key is revalidated before any channel queries. Authentication rejection
clears the saved connection; a temporary network failure keeps it available for retry.
Disconnect clears the credential and query cache. Credentials are never written to
`localStorage`; normal tab closure ends their session (browser session restore may
retain session storage). Theme and filter preferences are saved locally. Filters are
stored separately for each normalized service address and restored after reconnecting.
Only the selected filter key's opaque ID is stored, never the access credential.

## Features

- Separate connection page, responsive workspace, light and dark themes.
- Provider configuration order by default; API key selection keeps its configured
  channel set and order. Search, model/status/balance filters, explicit sorting
  and pagination compose without changing routing.
- Remember API key, model, endpoint, streaming mode, time window, search, status, balance and sort filters
  across reloads. Reset filters in one click; deleted keys/models require an
  explicit new selection. Reopening starts on the first page.
- Channel success rate, completed attempt count, request-to-dispatch p50 and first
  output p50/p95. Detailed drawers show upstream model, exact latest timing,
  samples, last success and balance details.
- Balance view with independent loading, three concurrent reads, five-minute
  cache and explicit unsupported/error/unknown states. Shared balances are never
  summed. An exhausted channel requires all upstream keys to be known and empty.
- Optional one-minute traffic buckets from the timeseries API; no synthetic charts.
- Optional 30-second metric refresh, paused while the page is in the background.
- Motion entrance/tab transitions, Radix accessible dialogs/tooltips, reduced
  motion support, keyboard navigation and a mobile navigation drawer.

## Metric scope

The workspace defaults to `endpoint=all&stream=all`; endpoint and streaming mode
can be filtered independently. Aggregate latency quantiles are calculated by the
backend from merged histograms, never by averaging group p50/p95 values. Attempts are not
user requests: retries count separately. Overall success rate is weighted by
completed attempts, never averaged from row percentages. API key selection
filters channel configuration; statistics still include all requests to those
channels. p50/p95 are histogram upper-bound estimates and must not be added
between stages. Metrics are held in backend instance memory; restarts produce a
partially covered window until new samples accumulate. Error responses never
turn into a fake zero balance or success rate.

## Platform endpoints

- `GET /v1/api-keys`
- `GET /v1/model-channels`
- `GET /v1/channel-metrics`
- `GET /v1/channel-metrics/timeseries`
- `GET /v1/channel-balances?provider=...`

Only masked key metadata is returned. Requests use an Authorization header;
credentials never enter URL parameters, query-cache keys or the static build.
The deployed frontend is static; its server does not proxy or store credentials.

## Build and verify

```sh
npm test
npm run build
npm run preview
```

Vite creates `dist/`. The Dockerfile builds with Node 24 and serves static files
with Nginx. `/healthz` returns a JSON health signal. GitHub Actions tests and builds
on push and pull requests. Dependencies are pinned through `package-lock.json`.

## Fugue source deployment

```sh
fugue --account yym68686@gmail.com --project uni-api-console \
  app create uni-api-console --github https://github.com/yym68686/uni-api-web \
  --branch main --build dockerfile --dockerfile Dockerfile --port 80
fugue --account yym68686@gmail.com --project uni-api-console app build uni-api-console
fugue --account yym68686@gmail.com --project uni-api-console app deploy uni-api-console
fugue --account yym68686@gmail.com --project uni-api-console app source sync resume uni-api-console
```

Fugue keeps `yym68686/uni-api-web` `main` as the durable source and automatically
builds/deploys changes. It is a separate app from the existing 0-0 product frontend.
No model service settings or production routing are changed by this application.
