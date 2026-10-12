# Polyzon Go SDK

The official Go client for the [Polyzon public API](https://api.polyzon.org): search
3D models across 22 catalogues (Thingiverse, Printables, MakerWorld, Cults3D and
more), fetch full model cards, and check your account and quota. The library
uses only the Go standard library, is safe for concurrent use, retries transient
failures, and keeps your token out of logs and error messages.

[Русская версия](README.ru.md)

- API documentation and sandbox: https://api.polyzon.org
- API tokens: https://polyzon.org/account/ (section «API»)
- Package reference: https://pkg.go.dev/github.com/PolyzonOrg/polyzon-go

## Contents

- [Installation](#installation)
- [Quick start](#quick-start)
- [Authentication](#authentication)
- [Methods](#methods)
- [Pagination](#pagination)
- [Batch requests and concurrency](#batch-requests-and-concurrency)
- [Response metadata](#response-metadata)
- [Errors](#errors)
- [Retries](#retries)
- [Timeouts and cancellation](#timeouts-and-cancellation)
- [Proxies](#proxies)
- [Custom HTTP client, CA certificates](#custom-http-client-ca-certificates)
- [Logging](#logging)
- [Rate limits and quota](#rate-limits-and-quota)
- [Configuration reference](#configuration-reference)
- [Security](#security)
- [Versioning](#versioning)
- [Requirements](#requirements)
- [License](#license)

## Installation

```sh
go get github.com/PolyzonOrg/polyzon-go
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/PolyzonOrg/polyzon-go"
)

func main() {
	client, err := polyzon.NewClient() // reads POLYZON_API_KEY
	if err != nil {
		log.Fatal(err)
	}

	page, err := client.Search(context.Background(), polyzon.SearchParams{Query: "дракон"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(page.Total, "models")
	for _, item := range page.Items {
		fmt.Println(item.Source, item.ID, item.Title)
	}
}
```

More runnable programs are in [`examples/`](examples): `quickstart`, `pagination`,
`batch` and `proxy` (`go run ./examples/quickstart дракон`).

## Authentication

Every request needs a personal API token (`pzk_…`). Issue one at
https://polyzon.org/account/ in the «API» section. The client reads it from the
`POLYZON_API_KEY` environment variable, or you pass it explicitly:

```go
client, err := polyzon.NewClient(polyzon.WithAPIKey(os.Getenv("MY_POLYZON_TOKEN")))
```

`NewClient` returns an error when the token is missing, longer than 512
characters, or contains anything but printable ASCII (spaces, line breaks and
control characters are rejected so they can never inject headers). The token is
masked whenever the client is printed (`polyzon.Client{baseURL: https://api.polyzon.org, apiKey: pzk_Q7xR…}`)
and never appears in errors or logs.

## Methods

All methods take a `context.Context` first and are safe to call from many
goroutines at once.

### Search

```go
page, err := client.Search(ctx, polyzon.SearchParams{
	Query:    "кашпо",                 // required, up to 200 characters, Russian or English
	Page:     1,                       // 1–100, default 1
	Size:     50,                      // 1–50, default 20; Page × Size ≤ 2400
	Sort:     polyzon.SortPopular,     // relevant (default), popular, newest, likes, makes, downloads
	Free:     true,                    // only free models
	Sources:  []string{polyzon.SourceThingiverse, polyzon.SourcePrintables},
	Formats:  []string{"stl", "3mf"},
	Licenses: []string{polyzon.LicenseCC0, polyzon.LicenseCCBY},
	Lang:     polyzon.LangEN,          // ru (default) or en; other texts are machine-translated
})
fmt.Println(page.Total, page.TotalRelation, page.HasMore, page.Partial)
for _, item := range page.Items {
	fmt.Println(item.Source, item.ID, item.Title, item.URL, item.License)
	if item.Price != nil {
		fmt.Println(item.Price.Type, item.Price.Display)
	}
	if item.Stats.Likes != nil {
		fmt.Println(*item.Stats.Likes, "likes")
	}
}
```

Zero values are not sent, so the server defaults apply. Lists are sent as one
comma-separated parameter. `Partial` reports that part of the catalogue did not
answer in time. `CorrectedQuery` and `DidYouMean` carry spelling suggestions.

Sources, licenses, sort orders and languages have constants (`polyzon.Source…`,
`polyzon.License…`, `polyzon.Sort…`, `polyzon.Lang…`), but every such field is a
plain `string`: new sources and licenses appear without a new SDK release.

### GetModel

```go
model, err := client.GetModel(ctx, polyzon.SourceThingiverse, "tv-1234567", polyzon.WithLang(polyzon.LangEN))
fmt.Println(model.Title, model.Description, model.Category)
fmt.Println(model.Images)         // URLs on the source's CDN
if model.License != nil {
	fmt.Println(model.License.ID, model.License.Raw)
}
for _, other := range model.Copies { // the same model on other sources
	fmt.Println(other.Source, other.ID)
}
```

`source` and `id` are the pair from search results and from
`polyzon.org/model/{source}/{id}`. They are validated before any request
(source `^[a-z0-9]{1,32}$`, id `^[A-Za-z0-9._-]{1,255}$`, not `.` or `..`); an
invalid value returns an error without spending quota. A model removed on the
right holder's request answers `polyzon.ErrGone`: delete your copy too.

### Me

```go
me, err := client.Me(ctx)
fmt.Println(me.Account.ID, me.Account.DisplayName, me.Token.Name, me.Token.Prefix)
```

### Limits

```go
limits, err := client.Limits(ctx)
fmt.Printf("%d/%d this minute, %d/%d today, resets at %v\n",
	limits.PerMinute.Used, limits.PerMinute.Limit,
	limits.Daily.Used, limits.Daily.Limit, time.Unix(limits.Daily.ResetAt, 0))
```

`Me` and `Limits` do not use the daily quota.

Counters (`Stats.Likes`, `Stats.Downloads`, …) are `*int64`: `nil` means the
source does not report the number. Optional texts (`Thumbnail`, `Creator`,
`Description`, …) are `""` when unknown. Dates are ISO-8601 strings exactly as the
source reported them. Unknown JSON fields are ignored and malformed values read
as empty, so new API fields never break your build or your parsing.

## Pagination

`SearchAll` returns an `iter.Seq2[polyzon.SearchItem, error]` that fetches pages
lazily, starting at `params.Page` (default 1), until the last page:

```go
for item, err := range client.SearchAll(ctx, polyzon.SearchParams{Query: "робот", Size: 50}) {
	if err != nil {
		return err // yielded once; the iteration then stops
	}
	fmt.Println(item.Title)
	if enough() {
		break // no further pages are requested
	}
}
```

Every page costs one request of the daily quota, so use `Size: 50` and `break`
when you have enough. The API serves at most 2400 results per query
(Page × Size) and no page past 100, so `SearchAll` stops after page 100 even
when `HasMore` is still true there (sizes under 24).

## Batch requests and concurrency

A `*polyzon.Client` is immutable and safe for concurrent use: create one per
process and share it between goroutines. Requests reuse a pool of keep-alive
connections (10 per host by default, see `WithMaxConnections`). Go has no
separate async API: run calls in goroutines and control them with contexts.

`GetModels` fetches many model cards with bounded parallelism and returns one
result per reference, in input order:

```go
refs := []polyzon.ModelRef{{Source: "thingiverse", ID: "tv-1234567"}, {Source: "printables", ID: "pr-123456"}}
results, err := client.GetModels(ctx, refs, polyzon.WithConcurrency(4), polyzon.WithLang(polyzon.LangRU))
if err != nil {
	log.Fatal(err) // only for invalid options, e.g. concurrency < 1
}
for _, res := range results {
	if res.Err != nil {
		fmt.Println(res.Ref, res.Err) // per-item failure
		continue
	}
	fmt.Println(res.Model.Title)
}
```

Concurrency defaults to 4. Once a request fails with `ErrAuthentication`,
`ErrPermissionDenied` or `ErrQuotaExceeded`, the references not started yet get
that same error without sending requests. Each fetched model costs one request of
the daily quota, and the per-minute limit (20 by default) applies to the batch.

## Response metadata

Every result carries a `Meta` field (excluded from JSON encoding):

```go
page, _ := client.Search(ctx, params)
fmt.Println(page.Meta.RequestID) // X-Request-ID, quote it in support requests
if rl := page.Meta.RateLimit; rl != nil { // the DAILY window from x-ratelimit-*
	fmt.Println(rl.Limit, rl.Remaining, time.Unix(rl.ResetAt, 0))
}
```

## Errors

Error answers of the API are `*polyzon.APIError` values. Classify them with
`errors.Is` and the sentinel errors, and read details with `errors.As`:

```go
model, err := client.GetModel(ctx, "thingiverse", "tv-1234567")
switch {
case errors.Is(err, polyzon.ErrNotFound):
	// no such model
case errors.Is(err, polyzon.ErrGone):
	// removed on the right holder's request: delete it on your side too
case errors.Is(err, polyzon.ErrQuotaExceeded):
	// the daily quota is spent until 00:00 UTC
case errors.Is(err, polyzon.ErrRateLimited):
	// per-minute limit, still hit after the automatic retries
case err != nil:
	var apiErr *polyzon.APIError
	if errors.As(err, &apiErr) {
		log.Printf("status %d, code %s, request %s: %s", apiErr.StatusCode, apiErr.Code, apiErr.RequestID, apiErr.Detail)
	}
}
```

| Sentinel | Matches | Codes |
| --- | --- | --- |
| `ErrBadRequest` | HTTP 400 | `bad_request` (`APIError.Param` names the parameter), `page_too_deep` |
| `ErrAuthentication` | HTTP 401 | `unauthorized`, `invalid_token` |
| `ErrPermissionDenied` | HTTP 403 | `account_action_required`: accept the current terms at polyzon.org/account |
| `ErrNotFound` | HTTP 404 | `not_found` |
| `ErrGone` | HTTP 410 | `gone` |
| `ErrRateLimited` | every HTTP 429 | `rate_limited`, `quota_exceeded` |
| `ErrQuotaExceeded` | HTTP 429 `quota_exceeded` (also matches `ErrRateLimited`) | `quota_exceeded` |
| `ErrServer` | HTTP 5xx | `unavailable`, `internal`, any |
| `ErrConnection` | network failure: DNS, TCP, TLS, proxy, reset (also timeouts) | — |
| `ErrTimeout` | an attempt ran out of time (also matches `ErrConnection`) | — |
| `ErrInvalidResponse` | a 2xx answer that is not a JSON object or exceeds 8 MiB | — |

Other statuses (another 4xx, or a 3xx, since redirects are never followed) are
an `*APIError` matching no sentinel. `APIError` fields: `StatusCode`, `Code`,
`Title`, `Detail`, `Param`, `RequestID`, `RetryAfter` (`time.Duration`, 0 when
absent) and `Header`. Its message reads
`polyzon: 429 rate_limited: Не больше 20 запросов в минуту. (request id …)`. A
non-JSON error body (an HTML page from the edge proxy) gives an empty `Code` and
`Detail` `HTTP <status>`; the raw body is never put into the message.

The `Code*` constants name the codes. Errors caused by your context are not
wrapped in any sentinel: they match `context.Canceled` or
`context.DeadlineExceeded`. An invalid argument (a bad model id, invalid
options) is a plain error returned before any request.

## Retries

Requests are retried up to 2 times (`polyzon.WithMaxRetries(n)`, 0 disables) on
connection errors, timeouts, HTTP 500, 502, 503, 504 and 429 `rate_limited`.
They are never retried on `quota_exceeded` or other 4xx answers. The wait is the
server's `Retry-After` (seconds or HTTP date) when present; a `Retry-After`
longer than 60 seconds is not waited for, and the error is returned at once.
Without it the wait is `min(8 s, 0.5 s × 2^attempt)` with random jitter (×0.5–1.0).
Waits end early when the context is cancelled.

## Timeouts and cancellation

Each attempt has a total time limit of 30 seconds, including connecting (at most
10 seconds) and reading the answer. Change it with `polyzon.WithTimeout`. A timed-out
attempt is retried and finally reported as `ErrTimeout`. For an overall deadline
across retries, use the context:

```go
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()
page, err := client.Search(ctx, params) // errors.Is(err, context.DeadlineExceeded) on expiry
```

A cancelled or expired context is never retried.

## Proxies

Without configuration the standard environment variables apply:
`HTTPS_PROXY`/`https_proxy`, `HTTP_PROXY`/`http_proxy` and `NO_PROXY`/`no_proxy`
(loopback hosts are never proxied through them). An explicit proxy overrides them:

```go
proxyURL, _ := url.Parse("http://user:password@proxy.internal:3128")
client, err := polyzon.NewClient(polyzon.WithProxy(proxyURL))
```

Supported schemes are `http://`, `https://` (TLS to the proxy), `socks5://` and
`socks5h://` (names resolved by the proxy), all native to Go's `net/http`.
Credentials in the URL are sent as Basic proxy authentication (percent-decoded)
and never appear in errors or logs. Requests to the https API go through a
CONNECT tunnel with end-to-end TLS, so the proxy never sees your token.
`polyzon.WithProxy(nil)` disables proxies, including the environment ones.

## Custom HTTP client, CA certificates

`polyzon.WithHTTPClient` sends requests through your `*http.Client`, for example
to trust a corporate CA, use mTLS or add tracing:

```go
pool, _ := x509.SystemCertPool()
pool.AppendCertsFromPEM(corporateCAPEM)
client, err := polyzon.NewClient(polyzon.WithHTTPClient(&http.Client{
	Transport: &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{RootCAs: pool},
	},
}))
```

The SDK uses a copy of your client that never follows redirects. Configure
proxies and pool size on its transport (`WithProxy` and `WithMaxConnections`
cannot be combined with it). TLS certificate verification is always on: the SDK
has no option to disable it and rejects an `*http.Transport` with
`InsecureSkipVerify`. `client.CloseIdleConnections()` closes idle
pooled connections, for example before a process sleeps for long.

## Logging

Pass a `*slog.Logger` to log requests at debug level: method, path with query,
status, attempt, duration and retry delays. Headers, and so the token, are never
logged.

```go
logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
client, err := polyzon.NewClient(polyzon.WithLogger(logger))
```

## Rate limits and quota

Quotas are per account, shared by all its tokens:

- 20 requests per minute for every method;
- 200 requests per UTC day for `Search` and `GetModel` (each page of `SearchAll`
  and each model of `GetModels` counts). `Me` and `Limits` do not use it.

The server charges the quota before it validates parameters, so even a rejected
request (400) costs one. A request over the per-minute limit fails with
`rate_limited` and is retried automatically after `Retry-After`; a spent daily
quota fails with `quota_exceeded` (resets at 00:00 UTC) and is not retried. Check
the remaining daily quota with `Meta.RateLimit` on any result or with
`client.Limits(ctx)`.

## Configuration reference

| Option | Default | Notes |
| --- | --- | --- |
| `WithAPIKey(key)` | env `POLYZON_API_KEY` | required |
| `WithBaseURL(url)` | env `POLYZON_BASE_URL`, else `https://api.polyzon.org` | https only; http only for `localhost`, `127.0.0.1`, `[::1]`; no credentials, query or fragment |
| `WithTimeout(d)` | 30 s per attempt | connect at most 10 s |
| `WithMaxRetries(n)` | 2 | 0 disables retries |
| `WithProxy(u)` | environment proxies | `nil` disables proxies |
| `WithHTTPClient(c)` | built-in transport | redirects are never followed |
| `WithMaxConnections(n)` | 10 | connections to the API at once |
| `WithUserAgent(s)` | — | appended to `polyzon-go/1.0.0 (go <version>; <os>/<arch>)` |
| `WithLogger(l)` | no logging | debug level |

Per call: `polyzon.WithLang(lang)` for `GetModel`/`GetModels`,
`polyzon.WithConcurrency(n)` for `GetModels`.

## Security

- **Never ship your token to browsers or mobile apps.** Anyone can extract it
  from there. Keep it on your server and call the API from there.
- The client refuses non-https base URLs (except loopback for local testing),
  never follows redirects (a redirect could carry the token to another origin),
  always verifies TLS certificates, caps response bodies at 8 MiB and validates
  path segments before building URLs.
- Revoke a leaked token at https://polyzon.org/account/ at once. Report
  vulnerabilities to security@polyzon.org, see [SECURITY.md](SECURITY.md).

## Versioning

The SDK follows [Semantic Versioning](https://semver.org/). Releases are git tags
(`v1.0.0`); breaking changes would ship as a new major version with a `/v2`
module path. See [CHANGELOG.md](CHANGELOG.md).

## Requirements

Go 1.23 or newer (for range-over-func iterators). No dependencies outside the
standard library.

> If you use the free Polyzon API, it would be great if you mentioned Polyzon (for example, with a link to [polyzon.org](https://polyzon.org)) on the pages of your project where the API is used.

## License

[MIT](LICENSE) © 2026 Polyzon
