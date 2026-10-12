package polyzon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the production API endpoint.
const DefaultBaseURL = "https://api.polyzon.org"

const (
	defaultTimeout        = 30 * time.Second
	defaultMaxRetries     = 2
	defaultMaxConnections = 10
	connectTimeout        = 10 * time.Second
	maxBodySize           = 8 << 20
	// maxRetryAfter is the longest Retry-After the client waits; a longer one
	// (the daily quota resets at midnight) fails immediately instead.
	maxRetryAfter = 60 * time.Second
)

// Client calls the Polyzon API. Create it with [NewClient]. A Client is immutable
// and safe for concurrent use by multiple goroutines; share one per process so
// requests reuse its pool of keep-alive connections.
type Client struct {
	apiKey     string
	baseURL    string
	http       *http.Client
	timeout    time.Duration
	maxRetries int
	userAgent  string
	logger     *slog.Logger
	// sleep waits before a retry; tests replace it so they never really sleep.
	sleep func(ctx context.Context, d time.Duration) error
}

// Option configures a [Client] in [NewClient].
type Option func(*settings) error

type settings struct {
	apiKey     *string
	baseURL    *string
	timeout    time.Duration
	maxRetries int
	proxy      *url.URL
	proxySet   bool
	httpClient *http.Client
	maxConns   int // 0: default
	userAgent  string
	logger     *slog.Logger
}

// WithAPIKey sets the API token, overriding the POLYZON_API_KEY environment
// variable. Tokens are issued at https://polyzon.org/account/ (section «API»).
func WithAPIKey(key string) Option {
	return func(s *settings) error {
		s.apiKey = &key
		return nil
	}
}

// WithBaseURL sets the API endpoint, overriding the POLYZON_BASE_URL environment
// variable and [DefaultBaseURL]. It must be https, or http on a loopback host
// (localhost, 127.0.0.1, [::1]) for local testing, without credentials, query
// or fragment.
func WithBaseURL(baseURL string) Option {
	return func(s *settings) error {
		s.baseURL = &baseURL
		return nil
	}
}

// WithTimeout sets the time limit of one request attempt, including reading the
// answer. The default is 30 seconds (10 of them at most for connecting). Use the
// context for an overall deadline across retries.
func WithTimeout(timeout time.Duration) Option {
	return func(s *settings) error {
		if timeout <= 0 {
			return errors.New("polyzon: timeout must be positive")
		}
		s.timeout = timeout
		return nil
	}
}

// WithMaxRetries sets how many times a failed request is retried (default 2);
// 0 disables retries. Retried are connection errors, timeouts, HTTP
// 500/502/503/504 and 429 rate_limited, after the server's Retry-After (a wait
// over 60 seconds is not retried) or an exponential backoff with jitter. A spent
// daily quota (429 quota_exceeded) is never retried.
func WithMaxRetries(retries int) Option {
	return func(s *settings) error {
		if retries < 0 {
			return errors.New("polyzon: max retries must not be negative")
		}
		s.maxRetries = retries
		return nil
	}
}

// WithProxy routes requests through the proxy at proxyURL: http://, https:// or
// socks5:// (socks5h:// resolves names on the proxy), with optional
// user:password for Basic proxy authentication. Requests to https:// endpoints
// are tunnelled with CONNECT, so the proxy never sees the token.
//
// WithProxy(nil) disables proxies. Without this option the standard environment
// variables HTTPS_PROXY, HTTP_PROXY and NO_PROXY apply (loopback hosts are never
// proxied through them).
func WithProxy(proxyURL *url.URL) Option {
	return func(s *settings) error {
		s.proxySet = true
		s.proxy = nil
		if proxyURL == nil {
			return nil
		}
		switch proxyURL.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return errors.New("polyzon: proxy scheme must be http, https, socks5 or socks5h")
		}
		if proxyURL.Host == "" {
			return errors.New("polyzon: proxy URL has no host")
		}
		u := *proxyURL
		s.proxy = &u
		return nil
	}
}

// WithHTTPClient makes the client send requests through a copy of httpClient,
// for a custom transport (CA bundle, mTLS, tracing). The copy never follows
// redirects, whatever httpClient.CheckRedirect says, since following one could
// leak the token to another origin. It cannot be combined with [WithProxy] or
// [WithMaxConnections]: configure the transport of httpClient instead. An
// [*http.Transport] with InsecureSkipVerify is rejected: add a custom CA to
// RootCAs rather than disabling certificate verification.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(s *settings) error {
		if httpClient == nil {
			return errors.New("polyzon: http client is nil")
		}
		s.httpClient = httpClient
		return nil
	}
}

// WithMaxConnections limits the connections to the API kept open at once
// (default 10). Requests above the limit wait for a free connection.
func WithMaxConnections(n int) Option {
	return func(s *settings) error {
		if n < 1 {
			return errors.New("polyzon: max connections must be at least 1")
		}
		s.maxConns = n
		return nil
	}
}

// WithUserAgent appends suffix (for example "my-bot/2.1") to the default
// User-Agent "polyzon-go/<version> (go <version>; <os>/<arch>)". The suffix must
// be 1–256 printable ASCII characters.
func WithUserAgent(suffix string) Option {
	return func(s *settings) error {
		if suffix == "" || len(suffix) > 256 || strings.IndexFunc(suffix, func(r rune) bool { return r < 0x20 || r > 0x7e }) >= 0 {
			return errors.New("polyzon: user agent suffix must be 1-256 printable ASCII characters")
		}
		s.userAgent = suffix
		return nil
	}
}

// WithLogger enables debug logging of requests: method, path with query, status,
// attempt, duration and retry delays. Headers, and so the token, are never
// logged. Records are emitted at [slog.LevelDebug].
func WithLogger(logger *slog.Logger) Option {
	return func(s *settings) error {
		s.logger = logger
		return nil
	}
}

// NewClient creates a client. Without [WithAPIKey] the token is read from the
// POLYZON_API_KEY environment variable; a missing or malformed token is an error.
func NewClient(opts ...Option) (*Client, error) {
	s := settings{timeout: defaultTimeout, maxRetries: defaultMaxRetries}
	for _, opt := range opts {
		if err := opt(&s); err != nil {
			return nil, err
		}
	}

	apiKey := os.Getenv("POLYZON_API_KEY")
	if s.apiKey != nil {
		apiKey = *s.apiKey
	}
	if err := validateAPIKey(apiKey); err != nil {
		return nil, err
	}

	rawBase := os.Getenv("POLYZON_BASE_URL")
	if rawBase == "" {
		rawBase = DefaultBaseURL
	}
	if s.baseURL != nil {
		rawBase = *s.baseURL
	}
	baseURL, err := parseBaseURL(rawBase)
	if err != nil {
		return nil, err
	}

	httpClient, err := s.newHTTPClient()
	if err != nil {
		return nil, err
	}

	userAgent := fmt.Sprintf("polyzon-go/%s (go %s; %s/%s)",
		Version, strings.TrimPrefix(runtime.Version(), "go"), runtime.GOOS, runtime.GOARCH)
	if s.userAgent != "" {
		userAgent += " " + s.userAgent
	}

	return &Client{
		apiKey:     apiKey,
		baseURL:    baseURL,
		http:       httpClient,
		timeout:    s.timeout,
		maxRetries: s.maxRetries,
		userAgent:  userAgent,
		logger:     s.logger,
		sleep:      sleep,
	}, nil
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (s *settings) newHTTPClient() (*http.Client, error) {
	if s.httpClient != nil {
		if s.proxySet || s.maxConns > 0 {
			return nil, errors.New("polyzon: WithProxy and WithMaxConnections cannot be combined with WithHTTPClient; configure its transport instead")
		}
		if t, ok := s.httpClient.Transport.(*http.Transport); ok && t.TLSClientConfig != nil && t.TLSClientConfig.InsecureSkipVerify {
			return nil, errors.New("polyzon: TLS certificate verification cannot be disabled; add your CA to TLSClientConfig.RootCAs instead")
		}
		c := *s.httpClient
		c.CheckRedirect = noRedirect
		return &c, nil
	}
	proxy := http.ProxyFromEnvironment
	if s.proxySet {
		proxy = nil
		if s.proxy != nil {
			proxy = http.ProxyURL(s.proxy)
		}
	}
	conns := s.maxConns
	if conns == 0 {
		conns = defaultMaxConnections
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 proxy,
			DialContext:           (&net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          conns,
			MaxIdleConnsPerHost:   conns,
			MaxConnsPerHost:       conns,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   connectTimeout,
			ExpectContinueTimeout: time.Second,
		},
		CheckRedirect: noRedirect,
	}, nil
}

// validateAPIKey keeps the token out of the error message: it is a secret.
func validateAPIKey(key string) error {
	if key == "" {
		return errors.New("polyzon: API key is missing: pass WithAPIKey or set POLYZON_API_KEY")
	}
	if len(key) > 512 {
		return errors.New("polyzon: API key is longer than 512 characters")
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return errors.New("polyzon: API key must contain only printable ASCII characters without spaces")
		}
	}
	return nil
}

// parseBaseURL never echoes the URL in its errors: it might carry credentials.
func parseBaseURL(raw string) (string, error) {
	invalid := func(reason string) (string, error) {
		return "", errors.New("polyzon: invalid base URL: " + reason)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return invalid("cannot be parsed")
	}
	if u.User != nil {
		return invalid("must not contain credentials")
	}
	if strings.ContainsAny(raw, "?#") {
		return invalid("must not contain a query or fragment")
	}
	if u.Opaque != "" || u.Hostname() == "" {
		return invalid("must be an absolute URL with a host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
		default:
			return invalid("http is allowed only for localhost, 127.0.0.1 and [::1]; use https")
		}
	default:
		return invalid("scheme must be https")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// String describes the client with the token masked to its first characters.
func (c Client) String() string {
	return "polyzon.Client{baseURL: " + c.baseURL + ", apiKey: " + maskKey(c.apiKey) + "}"
}

// Format implements [fmt.Formatter] so that every verb, including %+v and %#v,
// prints [Client.String] and never the token.
func (c Client) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, c.String())
}

func maskKey(key string) string {
	if len(key) < 16 {
		return "…"
	}
	return key[:8] + "…"
}

// CloseIdleConnections closes the idle keep-alive connections of the client's
// pool (of the custom client's transport with [WithHTTPClient]). The client
// stays usable.
func (c *Client) CloseIdleConnections() {
	c.http.CloseIdleConnections()
}

// get performs GET path?query with retries and returns the JSON object answered.
func (c *Client) get(ctx context.Context, path string, query url.Values) (object, Meta, error) {
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	for attempt := 0; ; attempt++ {
		body, meta, err := c.do(ctx, target, attempt)
		if err == nil {
			return body, meta, nil
		}
		if ctx.Err() != nil {
			return nil, Meta{}, fmt.Errorf("polyzon: GET %s: %w", path, ctx.Err())
		}
		delay, retry := retryDelay(err, attempt)
		if !retry || attempt >= c.maxRetries {
			return nil, Meta{}, err
		}
		c.debug(ctx, "polyzon: retrying", "method", http.MethodGet, "path", target, "attempt", attempt+1, "delay", delay)
		if err := c.sleep(ctx, delay); err != nil {
			return nil, Meta{}, fmt.Errorf("polyzon: GET %s: %w", path, err)
		}
	}
}

// do sends one attempt.
func (c *Client) do(ctx context.Context, target string, attempt int) (object, Meta, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+target, nil)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("polyzon: %w", err)
	}
	req.Header = http.Header{
		"Authorization": {"Bearer " + c.apiKey},
		"Accept":        {"application/json"},
		"User-Agent":    {c.userAgent},
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.debug(ctx, "polyzon: request failed", "method", http.MethodGet, "path", target, "attempt", attempt+1, "error", err)
		return nil, Meta{}, transportError(err)
	}
	defer resp.Body.Close()
	// Read one byte past the cap to detect an oversized body, then stop: closing
	// the body without draining it drops the connection instead of reading on.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	c.debug(ctx, "polyzon: request", "method", http.MethodGet, "path", target, "attempt", attempt+1,
		"status", resp.StatusCode, "duration", time.Since(start))
	if err != nil {
		return nil, Meta{}, transportError(err)
	}
	oversized := len(body) > maxBodySize
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if oversized {
			body = nil
		}
		return nil, Meta{}, newAPIError(resp, body)
	}
	if oversized {
		return nil, Meta{}, invalidResponse(resp, "body exceeds 8 MiB")
	}
	o, err := parseObject(body)
	if err != nil {
		return nil, Meta{}, invalidResponse(resp, err.Error())
	}
	return o, parseMeta(resp.Header), nil
}

func (c *Client) debug(ctx context.Context, msg string, args ...any) {
	if c.logger != nil {
		c.logger.DebugContext(ctx, msg, args...)
	}
}

func transportError(err error) error {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
		// The cause is not wrapped: context.DeadlineExceeded must mean the caller's deadline.
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	return fmt.Errorf("%w: %w", ErrConnection, err)
}

func invalidResponse(resp *http.Response, reason string) error {
	where := "HTTP " + strconv.Itoa(resp.StatusCode)
	if id := resp.Header.Get("X-Request-ID"); id != "" {
		where += ", request id " + id
	}
	return fmt.Errorf("%w: %s (%s)", ErrInvalidResponse, reason, where)
}

func parseMeta(h http.Header) Meta {
	meta := Meta{RequestID: h.Get("X-Request-ID")}
	limit, err1 := strconv.ParseInt(h.Get("X-Ratelimit-Limit"), 10, 64)
	remaining, err2 := strconv.ParseInt(h.Get("X-Ratelimit-Remaining"), 10, 64)
	resetAt, err3 := strconv.ParseInt(h.Get("X-Ratelimit-Reset"), 10, 64)
	if err1 == nil && err2 == nil && err3 == nil {
		meta.RateLimit = &RateLimit{Limit: limit, Remaining: remaining, ResetAt: resetAt}
	}
	return meta
}

// retryDelay decides whether err is worth another attempt and how long to wait.
// Retried: connection errors and timeouts, HTTP 500/502/503/504, and 429
// rate_limited (never quota_exceeded). Retry-After is honoured up to 60 s; a
// longer one means no retry. Otherwise the wait is min(8 s, 0.5 s × 2^attempt)
// with full jitter (× 0.5–1.0).
func retryDelay(err error, attempt int) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		case http.StatusTooManyRequests:
			if apiErr.Code != CodeRateLimited {
				return 0, false
			}
		default:
			return 0, false
		}
		if delay, ok := parseRetryAfter(apiErr.Header.Get("Retry-After")); ok {
			return delay, delay <= maxRetryAfter
		}
	} else if !errors.Is(err, ErrConnection) {
		return 0, false
	}
	backoff := 8 * time.Second
	if attempt < 4 {
		backoff = 500 * time.Millisecond << attempt
	}
	return time.Duration(float64(backoff) * (0.5 + rand.Float64()/2)), true
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
