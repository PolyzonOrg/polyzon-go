package polyzon

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// sentinel is an error kind; parent makes errors.Is see the kind hierarchy
// (a quota error is also a rate-limit error, a timeout is also a connection error).
type sentinel struct {
	msg    string
	parent error
}

func (s *sentinel) Error() string { return s.msg }
func (s *sentinel) Unwrap() error { return s.parent }

// Sentinel errors for [errors.Is]. Errors answered by the API are [*APIError]
// values matching one of the HTTP-status sentinels; transport problems match
// [ErrConnection] (and [ErrTimeout] when time ran out); malformed success
// answers match [ErrInvalidResponse].
//
// Errors caused by the caller's context (cancellation or its deadline) are not
// wrapped in any of these: they match [context.Canceled] or
// [context.DeadlineExceeded] instead.
var (
	// ErrBadRequest matches HTTP 400: a parameter is invalid (APIError.Param names
	// it) or the page is too deep (code page_too_deep).
	ErrBadRequest error = &sentinel{msg: "polyzon: bad request"}
	// ErrAuthentication matches HTTP 401: the token is missing, unknown or revoked.
	ErrAuthentication error = &sentinel{msg: "polyzon: authentication failed"}
	// ErrPermissionDenied matches HTTP 403: the token owner must accept the current
	// terms at https://polyzon.org/account/.
	ErrPermissionDenied error = &sentinel{msg: "polyzon: permission denied"}
	// ErrNotFound matches HTTP 404.
	ErrNotFound error = &sentinel{msg: "polyzon: not found"}
	// ErrGone matches HTTP 410: the model was removed on the right holder's
	// request. Delete your copy of it too.
	ErrGone error = &sentinel{msg: "polyzon: gone"}
	// ErrRateLimited matches every HTTP 429, including [ErrQuotaExceeded].
	ErrRateLimited error = &sentinel{msg: "polyzon: rate limited"}
	// ErrQuotaExceeded matches HTTP 429 with code quota_exceeded: the daily quota
	// is spent until 00:00 UTC. It also matches [ErrRateLimited].
	ErrQuotaExceeded error = &sentinel{msg: "polyzon: daily quota exceeded", parent: ErrRateLimited}
	// ErrServer matches HTTP 5xx answers.
	ErrServer error = &sentinel{msg: "polyzon: server error"}
	// ErrConnection matches network failures: DNS, TCP, TLS, proxy, reset
	// connections, and timeouts ([ErrTimeout]).
	ErrConnection error = &sentinel{msg: "polyzon: connection error"}
	// ErrTimeout matches a request attempt that ran out of time (see [WithTimeout]).
	// It also matches [ErrConnection].
	ErrTimeout error = &sentinel{msg: "polyzon: request timed out", parent: ErrConnection}
	// ErrInvalidResponse matches a success answer whose body is not a JSON
	// object or exceeds 8 MiB.
	ErrInvalidResponse error = &sentinel{msg: "polyzon: invalid response"}
)

// APIError is an error answer of the API (any non-2xx HTTP status, including a
// redirect, which the client never follows). The API answers
// application/problem+json; for other bodies (an HTML page from an edge proxy)
// Code is empty and Detail is "HTTP <status>".
//
// Use [errors.Is] with the sentinel errors to classify it and [errors.As] to read
// its fields:
//
//	var apiErr *polyzon.APIError
//	if errors.As(err, &apiErr) && apiErr.Code == polyzon.CodePageTooDeep { … }
type APIError struct {
	// StatusCode is the HTTP status code.
	StatusCode int
	// Code is the stable machine-readable error code (the Code* constants), or ""
	// when the body was not a problem document.
	Code string
	// Title is the short HTTP status title.
	Title string
	// Detail is the human-readable explanation (usually in Russian).
	Detail string
	// Param names the invalid query parameter for code bad_request, or "".
	Param string
	// RequestID is the X-Request-ID response header; quote it in support requests.
	RequestID string
	// RetryAfter is the Retry-After header as a duration, or 0 when absent.
	RetryAfter time.Duration
	// Header holds the response headers (for example x-ratelimit-*).
	Header http.Header
}

// Error formats the error as "polyzon: <status> <code>: <detail> (request id <id>)".
func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("polyzon: ")
	b.WriteString(strconv.Itoa(e.StatusCode))
	if e.Code != "" {
		b.WriteString(" ")
		b.WriteString(e.Code)
	}
	b.WriteString(": ")
	b.WriteString(e.Detail)
	if e.RequestID != "" {
		b.WriteString(" (request id ")
		b.WriteString(e.RequestID)
		b.WriteString(")")
	}
	return b.String()
}

// Unwrap returns the sentinel error matching the status code and error code,
// or nil for statuses without one (a 3xx, or another 4xx).
func (e *APIError) Unwrap() error {
	switch {
	case e.StatusCode == http.StatusBadRequest:
		return ErrBadRequest
	case e.StatusCode == http.StatusUnauthorized:
		return ErrAuthentication
	case e.StatusCode == http.StatusForbidden:
		return ErrPermissionDenied
	case e.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case e.StatusCode == http.StatusGone:
		return ErrGone
	case e.StatusCode == http.StatusTooManyRequests && e.Code == CodeQuotaExceeded:
		return ErrQuotaExceeded
	case e.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
	case e.StatusCode >= 500:
		return ErrServer
	}
	return nil
}

// newAPIError builds the error from a non-2xx answer. body is nil when it was
// over the size cap.
func newAPIError(resp *http.Response, body []byte) *APIError {
	e := &APIError{
		StatusCode: resp.StatusCode,
		Title:      http.StatusText(resp.StatusCode),
		RequestID:  resp.Header.Get("X-Request-ID"),
		Header:     resp.Header,
	}
	e.RetryAfter, _ = parseRetryAfter(resp.Header.Get("Retry-After"))
	if problem, err := parseObject(body); err == nil {
		e.Code = problem.str("code")
		e.Detail = problem.str("detail")
		e.Param = problem.str("param")
		if title := problem.str("title"); title != "" {
			e.Title = title
		}
	}
	if e.Detail == "" {
		e.Detail = "HTTP " + strconv.Itoa(resp.StatusCode)
	}
	return e
}

// parseRetryAfter reads delay-seconds or an HTTP-date. ok is false when absent
// or malformed. Huge values are clamped so the Duration cannot overflow.
func parseRetryAfter(value string) (delay time.Duration, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(min(seconds, 1<<31)) * time.Second, true
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(time.Until(at), 0), true
	}
	return 0, false
}
