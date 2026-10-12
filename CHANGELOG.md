# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-10-11

### Added

- Client for the Polyzon public API: `Search`, `SearchAll` (lazy `iter.Seq2`
  pagination), `GetModel`, `GetModels` (bounded-concurrency batch with
  short-circuit on authentication, permission and quota errors), `Me`, `Limits`.
- Options: API key (or `POLYZON_API_KEY`), base URL (or `POLYZON_BASE_URL`),
  per-attempt timeout, retries, explicit proxy (http, https, socks5) or the
  standard proxy environment, custom `*http.Client`, connection pool size,
  User-Agent suffix, `log/slog` debug logging.
- `*APIError` with sentinel errors for `errors.Is`, including `ErrQuotaExceeded`
  (also `ErrRateLimited`) and `ErrTimeout` (also `ErrConnection`).
- Automatic retries of connection errors, timeouts, HTTP 500/502/503/504 and
  429 `rate_limited`, honouring `Retry-After` up to 60 seconds.
- Response metadata (`X-Request-ID`, daily `x-ratelimit-*` window) on every result.
- Security defaults: token validation and masking, https-only base URL (http
  for loopback only), redirects never followed, 8 MiB response cap, client-side
  validation of model path segments.

[Unreleased]: https://github.com/PolyzonOrg/polyzon-go/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/PolyzonOrg/polyzon-go/releases/tag/v1.0.0
