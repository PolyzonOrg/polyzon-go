package polyzon

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testKey looks like a real token: pzk_ + 43 characters.
const testKey = "pzk_Q7xR2mVbSECRETSECRETSECRETSECRETSECRET123"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func problem(status int, code, detail string) []byte {
	return []byte(fmt.Sprintf(`{"type":"about:blank","title":%q,"status":%d,"code":%q,"detail":%q}`,
		http.StatusText(status), status, code, detail))
}

// reply writes a JSON answer; headers are name/value pairs.
func reply(w http.ResponseWriter, status int, body []byte, headers ...string) {
	for i := 0; i+1 < len(headers); i += 2 {
		w.Header().Set(headers[i], headers[i+1])
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// sleepRecorder replaces Client.sleep so retries never really wait.
type sleepRecorder struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (r *sleepRecorder) sleep(ctx context.Context, d time.Duration) error {
	r.mu.Lock()
	r.delays = append(r.delays, d)
	r.mu.Unlock()
	return ctx.Err()
}

func (r *sleepRecorder) get() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.delays...)
}

// newTestClient serves handler on a local server and returns a client for it.
func newTestClient(t *testing.T, handler http.HandlerFunc, opts ...Option) (*Client, *sleepRecorder) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := NewClient(append([]Option{WithAPIKey(testKey), WithBaseURL(server.URL)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.CloseIdleConnections)
	rec := &sleepRecorder{}
	c.sleep = rec.sleep
	return c, rec
}

func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestNewClientAPIKey(t *testing.T) {
	t.Setenv("POLYZON_BASE_URL", "")
	long := strings.Repeat("a", 512)
	tests := []struct {
		name    string
		env     string
		opts    []Option
		wantKey string
		wantErr bool
	}{
		{name: "from env", env: testKey, wantKey: testKey},
		{name: "option overrides env", env: "pzk_env", opts: []Option{WithAPIKey(testKey)}, wantKey: testKey},
		{name: "missing", wantErr: true},
		{name: "explicit empty", env: testKey, opts: []Option{WithAPIKey("")}, wantErr: true},
		{name: "space", opts: []Option{WithAPIKey("pzk_abc def")}, wantErr: true},
		{name: "newline", opts: []Option{WithAPIKey("pzk_abc\r\nX-Evil: 1")}, wantErr: true},
		{name: "nul", opts: []Option{WithAPIKey("pzk_abc\x00")}, wantErr: true},
		{name: "non-ascii", opts: []Option{WithAPIKey("pzk_ключ")}, wantErr: true},
		{name: "512 chars", opts: []Option{WithAPIKey(long)}, wantKey: long},
		{name: "513 chars", opts: []Option{WithAPIKey(long + "a")}, wantErr: true},
		{name: "other format accepted", opts: []Option{WithAPIKey("token-v2.abc")}, wantKey: "token-v2.abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("POLYZON_API_KEY", tt.env)
			c, err := NewClient(tt.opts...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				if strings.Contains(err.Error(), "abc") || strings.Contains(err.Error(), "ключ") {
					t.Fatalf("error leaks the key: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.apiKey != tt.wantKey {
				t.Fatalf("key %q, want %q", c.apiKey, tt.wantKey)
			}
		})
	}
}

func TestNewClientBaseURL(t *testing.T) {
	tests := []struct {
		name, env, option string
		useOption         bool
		want              string
	}{
		{name: "default", want: DefaultBaseURL},
		{name: "env", env: "https://api.example.com", want: "https://api.example.com"},
		{name: "option overrides env", env: "https://env.example.com", option: "https://opt.example.com", useOption: true, want: "https://opt.example.com"},
		{name: "trailing slashes stripped", option: "https://api.polyzon.org//", useOption: true, want: "https://api.polyzon.org"},
		{name: "path prefix kept", option: "https://gw.example.com/polyzon/", useOption: true, want: "https://gw.example.com/polyzon"},
		{name: "http localhost", option: "http://localhost:8080", useOption: true, want: "http://localhost:8080"},
		{name: "http 127.0.0.1", option: "http://127.0.0.1:8080", useOption: true, want: "http://127.0.0.1:8080"},
		{name: "http ::1", option: "http://[::1]:8080", useOption: true, want: "http://[::1]:8080"},
		{name: "uppercase scheme", option: "HTTPS://api.polyzon.org", useOption: true, want: "https://api.polyzon.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("POLYZON_BASE_URL", tt.env)
			opts := []Option{WithAPIKey(testKey)}
			if tt.useOption {
				opts = append(opts, WithBaseURL(tt.option))
			}
			c, err := NewClient(opts...)
			if err != nil {
				t.Fatal(err)
			}
			if c.baseURL != tt.want {
				t.Fatalf("base URL %q, want %q", c.baseURL, tt.want)
			}
		})
	}

	invalid := []string{
		"",
		"api.polyzon.org",
		"http://api.polyzon.org",
		"http://10.0.0.1",
		"http://127.0.0.2",
		"ftp://api.polyzon.org",
		"https://user:hunter2pass@api.polyzon.org",
		"https://api.polyzon.org/?x=1",
		"https://api.polyzon.org/?",
		"https://api.polyzon.org/#frag",
		"https:///nohost",
		"https:opaque",
		"https://api.polyzon.org/\n",
		"://bad",
	}
	for _, raw := range invalid {
		t.Run("invalid "+raw, func(t *testing.T) {
			_, err := NewClient(WithAPIKey(testKey), WithBaseURL(raw))
			if err == nil {
				t.Fatalf("NewClient(%q): want error", raw)
			}
			if strings.Contains(err.Error(), "hunter2pass") {
				t.Fatalf("error leaks credentials: %v", err)
			}
		})
	}
}

func TestOptionValidation(t *testing.T) {
	tests := map[string]Option{
		"zero timeout":          WithTimeout(0),
		"negative timeout":      WithTimeout(-time.Second),
		"negative retries":      WithMaxRetries(-1),
		"zero connections":      WithMaxConnections(0),
		"empty user agent":      WithUserAgent(""),
		"user agent CRLF":       WithUserAgent("bot\r\nX-Evil: 1"),
		"user agent non-ascii":  WithUserAgent("бот"),
		"user agent too long":   WithUserAgent(strings.Repeat("a", 257)),
		"nil http client":       WithHTTPClient(nil),
		"proxy scheme":          WithProxy(&url.URL{Scheme: "ftp", Host: "proxy:21"}),
		"proxy without host":    WithProxy(&url.URL{Scheme: "http"}),
		"socks4 is unsupported": WithProxy(&url.URL{Scheme: "socks4", Host: "proxy:1080"}),
	}
	for name, opt := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewClient(WithAPIKey(testKey), opt); err == nil {
				t.Fatal("want error")
			}
		})
	}

	proxy := &url.URL{Scheme: "http", Host: "proxy:3128"}
	for name, opts := range map[string][]Option{
		"http client with proxy":                {WithHTTPClient(&http.Client{}), WithProxy(proxy)},
		"http client with nil proxy":            {WithHTTPClient(&http.Client{}), WithProxy(nil)},
		"http client with connections":          {WithMaxConnections(5), WithHTTPClient(&http.Client{})},
		"http client skipping TLS verification": {WithHTTPClient(&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}})},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewClient(append(opts, WithAPIKey(testKey))...); err == nil {
				t.Fatal("want error")
			}
		})
	}

	c, err := NewClient(WithAPIKey(testKey), WithTimeout(time.Second), WithMaxRetries(0), WithMaxConnections(3),
		WithProxy(&url.URL{Scheme: "socks5", Host: "proxy:1080"}), WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.timeout != time.Second || c.maxRetries != 0 {
		t.Fatalf("timeout %v retries %d", c.timeout, c.maxRetries)
	}
	tr := c.http.Transport.(*http.Transport)
	if tr.MaxConnsPerHost != 3 || tr.MaxIdleConnsPerHost != 3 || tr.Proxy == nil {
		t.Fatalf("transport not configured: %+v", tr)
	}
}

func TestDefaultTransport(t *testing.T) {
	c, err := NewClient(WithAPIKey(testKey))
	if err != nil {
		t.Fatal(err)
	}
	tr := c.http.Transport.(*http.Transport)
	if tr.Proxy == nil {
		t.Fatal("environment proxies must apply by default")
	}
	if tr.MaxConnsPerHost != defaultMaxConnections || c.timeout != defaultTimeout || c.maxRetries != defaultMaxRetries {
		t.Fatal("unexpected defaults")
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification must stay on")
	}

	c, err = NewClient(WithAPIKey(testKey), WithProxy(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("WithProxy(nil) must disable proxies")
	}
}

func TestTokenNeverPrinted(t *testing.T) {
	c, err := NewClient(WithAPIKey(testKey), WithBaseURL(DefaultBaseURL))
	if err != nil {
		t.Fatal(err)
	}
	outputs := []string{
		fmt.Sprint(c), fmt.Sprint(*c),
		fmt.Sprintf("%v %+v %#v %s %q %x", c, c, c, c, c, c),
		fmt.Sprintf("%v %+v %#v", *c, *c, *c),
		c.String(),
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("client", "c", c)
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("client", "c", c)
	outputs = append(outputs, buf.String())
	for _, out := range outputs {
		if strings.Contains(out, "SECRET") {
			t.Fatalf("token leaked: %s", out)
		}
	}
	if want := "polyzon.Client{baseURL: https://api.polyzon.org, apiKey: pzk_Q7xR…}"; c.String() != want {
		t.Fatalf("String() = %q, want %q", c.String(), want)
	}
	if got := maskKey("short"); got != "…" {
		t.Fatalf("short key masked as %q", got)
	}
}

func TestRequestHeaders(t *testing.T) {
	var got http.Header
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		reply(w, 200, fixture(t, "me.json"))
	}, WithUserAgent("my-bot/2.1 (+https://example.com)"))
	if _, err := c.Me(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.Get("Authorization") != "Bearer "+testKey {
		t.Fatalf("Authorization %q", got.Get("Authorization"))
	}
	if got.Get("Accept") != "application/json" {
		t.Fatalf("Accept %q", got.Get("Accept"))
	}
	ua := regexp.MustCompile(`^polyzon-go/1\.0\.0 \(go [^;]+; [a-z0-9]+/[a-z0-9]+\) my-bot/2\.1 \(\+https://example\.com\)$`)
	if !ua.MatchString(got.Get("User-Agent")) {
		t.Fatalf("User-Agent %q", got.Get("User-Agent"))
	}
	for name := range got {
		switch name {
		case "Authorization", "Accept", "User-Agent", "Accept-Encoding":
		default:
			t.Errorf("unexpected header %s", name)
		}
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	var followed atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Add(1)
		reply(w, 200, fixture(t, "me.json"))
	}))
	defer elsewhere.Close()
	redirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v1/me", http.StatusFound)
	}
	follows := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	for name, opts := range map[string][]Option{"default": nil, "custom http client": {WithHTTPClient(follows)}} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestClient(t, redirect, opts...)
			_, err := c.Me(context.Background())
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusFound {
				t.Fatalf("want APIError 302, got %v", err)
			}
			if apiErr.Unwrap() != nil {
				t.Fatalf("a redirect matches no sentinel, got %v", apiErr.Unwrap())
			}
			if followed.Load() != 0 {
				t.Fatal("redirect was followed")
			}
		})
	}
	if follows.CheckRedirect == nil {
		t.Fatal("the caller's http.Client must not be modified")
	}
}

func TestBodyCap(t *testing.T) {
	big := append([]byte(`{"query":"`), bytes.Repeat([]byte("a"), maxBodySize)...)
	big = append(big, `"}`...)

	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write(big)
	_ = zw.Close()

	t.Run("plain", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, big) })
		if _, err := c.Search(context.Background(), SearchParams{Query: "a"}); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("want ErrInvalidResponse, got %v", err)
		}
	})
	t.Run("gzip is capped after decompression", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept-Encoding") != "gzip" {
				t.Error("transport did not ask for gzip")
			}
			reply(w, 200, zipped.Bytes(), "Content-Encoding", "gzip")
		})
		if _, err := c.Search(context.Background(), SearchParams{Query: "a"}); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("want ErrInvalidResponse, got %v", err)
		}
	})
	t.Run("oversized error body", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 404, big) })
		_, err := c.Me(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Detail != "HTTP 404" || !errors.Is(err, ErrNotFound) {
			t.Fatalf("want plain 404, got %v", err)
		}
	})
	t.Run("exactly at the cap", func(t *testing.T) {
		exact := append([]byte(`{"query":"`), bytes.Repeat([]byte("a"), maxBodySize-12)...)
		exact = append(exact, `"}`...)
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, exact) })
		page, err := c.Search(context.Background(), SearchParams{Query: "a"})
		if err != nil || len(page.Query) != maxBodySize-12 {
			t.Fatalf("body of exactly 8 MiB must pass: %v", err)
		}
	})
}

func TestInvalidResponse(t *testing.T) {
	bodies := map[string]string{
		"html":             "<html><body>Bad gateway</body></html>",
		"array":            `[{"query":"x"}]`,
		"null":             "null",
		"string":           `"ok"`,
		"empty":            "",
		"truncated":        `{"query":"x"`,
		"trailing garbage": `{"query":"x"} trailing`,
		"two values":       `{} {}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			c, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				reply(w, 200, []byte(body), "X-Request-ID", "req-1")
			})
			_, err := c.Me(context.Background())
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("want ErrInvalidResponse, got %v", err)
			}
			if !strings.Contains(err.Error(), "request id req-1") || strings.Contains(err.Error(), "<html>") {
				t.Fatalf("message %q", err)
			}
			if len(rec.get()) != 0 {
				t.Fatal("an invalid response must not be retried")
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	var calls atomic.Int32
	c, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}, WithTimeout(50*time.Millisecond), WithMaxRetries(1))
	_, err := c.Me(context.Background())
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, ErrConnection) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	// Under load an attempt may time out before it even reaches the server, so the
	// retry is proven by the wait between attempts, not by the server-side count.
	if n := calls.Load(); n > 2 || len(rec.get()) != 1 {
		t.Fatalf("timeouts must be retried once: %d calls, %d sleeps", n, len(rec.get()))
	}
}

func TestTimeoutWhileReadingBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"query":`))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}, WithTimeout(50*time.Millisecond), WithMaxRetries(0))
	if _, err := c.Me(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestConnectionError(t *testing.T) {
	c, err := NewClient(WithAPIKey(testKey), WithBaseURL("http://"+closedAddr(t)))
	if err != nil {
		t.Fatal(err)
	}
	rec := &sleepRecorder{}
	c.sleep = rec.sleep
	_, err = c.Me(context.Background())
	if !errors.Is(err, ErrConnection) || errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrConnection, got %v", err)
	}
	if len(rec.get()) != defaultMaxRetries {
		t.Fatalf("connection errors must be retried, slept %v", rec.get())
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatal("token leaked")
	}
}

type step struct {
	status     int
	code       string
	retryAfter string
}

func TestRetries(t *testing.T) {
	ok := step{status: 200}
	tests := []struct {
		name       string
		steps      []step // the last step repeats
		opts       []Option
		wantCalls  int32
		wantSleeps []time.Duration // nil: backoff, checked by range
		wantErr    error
	}{
		{name: "503 then ok", steps: []step{{503, "unavailable", "5"}, ok}, wantCalls: 2, wantSleeps: []time.Duration{5 * time.Second}},
		{name: "502 504 then ok", steps: []step{{502, "", ""}, {504, "", ""}, ok}, wantCalls: 3},
		{name: "500 exhausts retries", steps: []step{{500, "internal", ""}}, wantCalls: 3, wantErr: ErrServer},
		{name: "rate limited then ok", steps: []step{{429, "rate_limited", "3"}, ok}, wantCalls: 2, wantSleeps: []time.Duration{3 * time.Second}},
		{name: "retry-after zero", steps: []step{{429, "rate_limited", "0"}, ok}, wantCalls: 2, wantSleeps: []time.Duration{0}},
		{name: "retry-after 60 is honoured", steps: []step{{503, "unavailable", "60"}, ok}, wantCalls: 2, wantSleeps: []time.Duration{time.Minute}},
		{name: "retry-after over 60 fails at once", steps: []step{{429, "rate_limited", "61"}}, wantCalls: 1, wantErr: ErrRateLimited},
		{name: "retry-after date in the past", steps: []step{{503, "unavailable", "Mon, 02 Jan 2006 15:04:05 GMT"}, ok}, wantCalls: 2, wantSleeps: []time.Duration{0}},
		{name: "retry-after date far ahead", steps: []step{{503, "unavailable", "Fri, 01 Jan 2100 00:00:00 GMT"}}, wantCalls: 1, wantErr: ErrServer},
		{name: "malformed retry-after falls back to backoff", steps: []step{{503, "unavailable", "soon"}, ok}, wantCalls: 2},
		{name: "quota exceeded is not retried", steps: []step{{429, "quota_exceeded", "3600"}}, wantCalls: 1, wantErr: ErrQuotaExceeded},
		{name: "quota exceeded with short retry-after", steps: []step{{429, "quota_exceeded", "5"}}, wantCalls: 1, wantErr: ErrQuotaExceeded},
		{name: "429 without code is not retried", steps: []step{{429, "", ""}}, wantCalls: 1, wantErr: ErrRateLimited},
		{name: "400 is not retried", steps: []step{{400, "bad_request", ""}}, wantCalls: 1, wantErr: ErrBadRequest},
		{name: "401 is not retried", steps: []step{{401, "invalid_token", ""}}, wantCalls: 1, wantErr: ErrAuthentication},
		{name: "501 is not retried", steps: []step{{501, "", ""}}, wantCalls: 1, wantErr: ErrServer},
		{name: "retries disabled", steps: []step{{503, "unavailable", "5"}}, opts: []Option{WithMaxRetries(0)}, wantCalls: 1, wantErr: ErrServer},
		{name: "more retries", steps: []step{{503, "unavailable", "1"}}, opts: []Option{WithMaxRetries(4)}, wantCalls: 5, wantErr: ErrServer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			c, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				n := int(calls.Add(1)) - 1
				s := tt.steps[min(n, len(tt.steps)-1)]
				if s.status == 200 {
					reply(w, 200, fixture(t, "limits.json"))
					return
				}
				headers := []string{"Content-Type", "application/problem+json"}
				if s.retryAfter != "" {
					headers = append(headers, "Retry-After", s.retryAfter)
				}
				body := problem(s.status, s.code, "detail")
				if s.code == "" {
					body = []byte("<html>edge error</html>")
				}
				reply(w, s.status, body, headers...)
			}, tt.opts...)
			limits, err := c.Limits(context.Background())
			if tt.wantErr == nil {
				if err != nil || limits.Daily.Limit != 200 {
					t.Fatalf("want success, got %v", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("want %v, got %v", tt.wantErr, err)
			}
			if calls.Load() != tt.wantCalls {
				t.Fatalf("%d calls, want %d", calls.Load(), tt.wantCalls)
			}
			sleeps := rec.get()
			if len(sleeps) != int(tt.wantCalls)-1 {
				t.Fatalf("%d sleeps for %d calls", len(sleeps), tt.wantCalls)
			}
			if tt.wantSleeps != nil {
				for i, want := range tt.wantSleeps {
					if sleeps[i] != want {
						t.Fatalf("sleep %d = %v, want %v", i, sleeps[i], want)
					}
				}
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	err := fmt.Errorf("%w: reset", ErrConnection)
	for attempt, base := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second} {
		for range 50 {
			d, ok := retryDelay(err, attempt)
			if !ok || d < base/2 || d > base {
				t.Fatalf("attempt %d: delay %v outside [%v, %v]", attempt, d, base/2, base)
			}
		}
	}
	if _, ok := retryDelay(errors.New("other"), 0); ok {
		t.Fatal("unknown errors must not be retried")
	}
	if _, ok := retryDelay(fmt.Errorf("%w: x", ErrInvalidResponse), 0); ok {
		t.Fatal("invalid responses must not be retried")
	}
	if d, ok := retryDelay(err, 1000); !ok || d > 8*time.Second {
		t.Fatalf("large attempt: %v %v", d, ok)
	}
}

func TestParseRetryAfter(t *testing.T) {
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	tests := []struct {
		in     string
		min    time.Duration
		max    time.Duration
		wantOK bool
	}{
		{"", 0, 0, false},
		{"abc", 0, 0, false},
		{"-5", 0, 0, false},
		{" 7 ", 7 * time.Second, 7 * time.Second, true},
		{"99999999999999", time.Duration(1<<31) * time.Second, time.Duration(1<<31) * time.Second, true},
		{future, 25 * time.Second, 30 * time.Second, true},
		{"Mon, 02 Jan 2006 15:04:05 GMT", 0, 0, true},
	}
	for _, tt := range tests {
		d, ok := parseRetryAfter(tt.in)
		if ok != tt.wantOK || d < tt.min || d > tt.max {
			t.Errorf("parseRetryAfter(%q) = %v, %v", tt.in, d, ok)
		}
	}
}

func TestCallerContext(t *testing.T) {
	t.Run("cancel during request", func(t *testing.T) {
		arrived := make(chan struct{}, 1)
		var calls atomic.Int32
		c, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			arrived <- struct{}{}
			<-r.Context().Done()
		})
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-arrived; cancel() }()
		_, err := c.Me(ctx)
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrConnection) {
			t.Fatalf("want context.Canceled only, got %v", err)
		}
		if calls.Load() != 1 || len(rec.get()) != 0 {
			t.Fatal("a cancelled request must not be retried")
		}
	})
	t.Run("caller deadline", func(t *testing.T) {
		c, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := c.Me(ctx)
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout) {
			t.Fatalf("want context.DeadlineExceeded only, got %v", err)
		}
		if len(rec.get()) != 0 {
			t.Fatal("must not retry after the caller's deadline")
		}
	})
	t.Run("already cancelled", func(t *testing.T) {
		var calls atomic.Int32
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.Me(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
		if calls.Load() != 0 {
			t.Fatal("request sent with a cancelled context")
		}
	})
	t.Run("cancel during retry wait", func(t *testing.T) {
		arrived := make(chan struct{}, 1)
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, 503, problem(503, "unavailable", "down"), "Retry-After", "30")
			arrived <- struct{}{}
		})
		c.sleep = sleep // the real one: it must wake up on cancel
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-arrived; time.Sleep(20 * time.Millisecond); cancel() }()
		start := time.Now()
		_, err := c.Me(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
		if time.Since(start) > 10*time.Second {
			t.Fatal("the retry wait ignored the cancellation")
		}
	})
}

func TestSleep(t *testing.T) {
	if err := sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			reply(w, 503, problem(503, "unavailable", "down"), "Retry-After", "2")
			return
		}
		reply(w, 200, fixture(t, "search.json"))
	}, WithLogger(logger))
	if _, err := c.Search(context.Background(), SearchParams{Query: "дракон", Size: 5}); err != nil {
		t.Fatal(err)
	}
	logs := buf.String()
	for _, want := range []string{"method=GET", `path="/v1/search?q=%D0%B4`, "status=503", "status=200", "attempt=1", "attempt=2", "delay=2s"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs miss %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "SECRET") || strings.Contains(logs, "Bearer") {
		t.Fatalf("logs leak the token:\n%s", logs)
	}

	buf.Reset()
	c, _ = newTestClient(t, nil, WithLogger(logger), WithBaseURL("http://"+closedAddr(t)), WithMaxRetries(0))
	_, _ = c.Me(context.Background())
	if !strings.Contains(buf.String(), "request failed") {
		t.Fatalf("transport failure not logged:\n%s", buf.String())
	}
}

func TestConcurrentUse(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		reply(w, 200, fixture(t, "search.json"))
	})
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Search(context.Background(), SearchParams{Query: "дракон"}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if calls.Load() != 32 {
		t.Fatalf("%d calls", calls.Load())
	}
}
