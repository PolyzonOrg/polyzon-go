package polyzon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestErrorMapping(t *testing.T) {
	all := []error{ErrBadRequest, ErrAuthentication, ErrPermissionDenied, ErrNotFound, ErrGone,
		ErrRateLimited, ErrQuotaExceeded, ErrServer, ErrConnection, ErrTimeout, ErrInvalidResponse}
	tests := []struct {
		status int
		code   string
		want   []error
	}{
		{400, CodeBadRequest, []error{ErrBadRequest}},
		{400, CodePageTooDeep, []error{ErrBadRequest}},
		{401, CodeUnauthorized, []error{ErrAuthentication}},
		{401, CodeInvalidToken, []error{ErrAuthentication}},
		{403, CodeAccountActionRequired, []error{ErrPermissionDenied}},
		{404, CodeNotFound, []error{ErrNotFound}},
		{410, CodeGone, []error{ErrGone}},
		{429, CodeRateLimited, []error{ErrRateLimited}},
		{429, CodeQuotaExceeded, []error{ErrQuotaExceeded, ErrRateLimited}},
		{500, CodeInternal, []error{ErrServer}},
		{503, CodeUnavailable, []error{ErrServer}},
		{502, "", []error{ErrServer}},
		{599, "anything", []error{ErrServer}},
		{405, "", nil},
		{418, "teapot", nil},
		{301, "", nil},
	}
	for _, tt := range tests {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, tt.status, problem(tt.status, tt.code, "detail"), "Content-Type", "application/problem+json")
		}, WithMaxRetries(0))
		_, err := c.Me(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status || apiErr.Code != tt.code {
			t.Fatalf("%d %s: got %v", tt.status, tt.code, err)
		}
		for _, sentinel := range all {
			want := false
			for _, w := range tt.want {
				want = want || w == sentinel
			}
			if errors.Is(err, sentinel) != want {
				t.Errorf("%d %s: errors.Is(%v) = %v", tt.status, tt.code, sentinel, !want)
			}
		}
	}
}

func TestAPIErrorFields(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 400, []byte(`{"type":"about:blank","title":"Bad Request","status":400,"code":"bad_request",`+
			`"detail":"size: от 1 до 50","param":"size","extra":{"ignored":true}}`),
			"Content-Type", "application/problem+json", "X-Request-ID", "req-42", "Retry-After", "7")
	})
	_, err := c.Search(context.Background(), SearchParams{Query: "x", Size: 99})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want APIError, got %v", err)
	}
	if apiErr.Title != "Bad Request" || apiErr.Detail != "size: от 1 до 50" || apiErr.Param != "size" ||
		apiErr.RequestID != "req-42" || apiErr.RetryAfter != 7*time.Second || apiErr.Header.Get("Retry-After") != "7" {
		t.Fatalf("fields %+v", apiErr)
	}
	if want := "polyzon: 400 bad_request: size: от 1 до 50 (request id req-42)"; err.Error() != want {
		t.Fatalf("message %q, want %q", err.Error(), want)
	}
}

func TestNonProblemErrorBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 502, []byte("<html><body><h1>502 Bad Gateway</h1>DDoS-Guard</body></html>"), "Content-Type", "text/html")
	}, WithMaxRetries(0))
	_, err := c.Me(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !errors.Is(err, ErrServer) {
		t.Fatalf("want server APIError, got %v", err)
	}
	if apiErr.Code != "" || apiErr.Detail != "HTTP 502" || apiErr.Title != "Bad Gateway" || apiErr.RetryAfter != 0 {
		t.Fatalf("fields %+v", apiErr)
	}
	if err.Error() != "polyzon: 502: HTTP 502" || strings.Contains(err.Error(), "html") {
		t.Fatalf("message %q", err.Error())
	}
}

func TestProblemWithWrongTypes(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 404, []byte(`{"code":404,"detail":["x"],"title":null,"param":{}}`))
	})
	_, err := c.Me(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "" || apiErr.Detail != "HTTP 404" || apiErr.Title != "Not Found" {
		t.Fatalf("got %+v", err)
	}
}

func TestSentinelHierarchy(t *testing.T) {
	if !errors.Is(ErrQuotaExceeded, ErrRateLimited) || errors.Is(ErrRateLimited, ErrQuotaExceeded) {
		t.Fatal("quota errors are rate-limit errors, not the reverse")
	}
	if !errors.Is(ErrTimeout, ErrConnection) || errors.Is(ErrConnection, ErrTimeout) {
		t.Fatal("timeouts are connection errors, not the reverse")
	}
	if ErrNotFound.Error() != "polyzon: not found" {
		t.Fatalf("message %q", ErrNotFound)
	}
}
