package polyzon

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProxyAbsoluteForm(t *testing.T) {
	type seen struct{ uri, proxyAuth, auth string }
	got := make(chan seen, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{r.RequestURI, r.Header.Get("Proxy-Authorization"), r.Header.Get("Authorization")}
		reply(w, 200, fixture(t, "me.json"))
	}))
	defer proxy.Close()

	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("user", "p@ss word:1")
	// localhost would bypass environment proxies, but an explicit proxy applies.
	c, err := NewClient(WithAPIKey(testKey), WithBaseURL("http://localhost:9"), WithProxy(proxyURL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if me.Account.DisplayName != "Алексей" {
		t.Fatalf("answer %+v", me)
	}
	s := <-got
	if s.uri != "http://localhost:9/v1/me" {
		t.Fatalf("proxy got request-target %q, want absolute form", s.uri)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:p@ss word:1")); s.proxyAuth != want {
		t.Fatalf("Proxy-Authorization %q, want %q", s.proxyAuth, want)
	}
	if s.auth != "Bearer "+testKey {
		t.Fatalf("Authorization %q", s.auth)
	}
}

func TestProxyConnectTunnel(t *testing.T) {
	var targetAuth string
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetAuth = r.Header.Get("Authorization")
		reply(w, 200, fixture(t, "limits.json"))
	}))
	defer target.Close()

	type seen struct{ method, host, proxyAuth, auth string }
	got := make(chan seen, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{r.Method, r.Host, r.Header.Get("Proxy-Authorization"), r.Header.Get("Authorization")}
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() { _, _ = io.Copy(upstream, buf); _ = upstream.Close() }()
		_, _ = io.Copy(conn, upstream)
	}))
	defer proxy.Close()

	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("user", "secret")
	c, err := NewClient(WithAPIKey(testKey), WithBaseURL(target.URL), WithProxy(proxyURL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	// Trust the test server's certificate; verification itself stays on.
	c.http.Transport.(*http.Transport).TLSClientConfig = target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	defer c.CloseIdleConnections()

	limits, err := c.Limits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if limits.Daily.Limit != 200 {
		t.Fatalf("limits %+v", limits)
	}
	s := <-got
	if s.method != http.MethodConnect || s.host != strings.TrimPrefix(target.URL, "https://") {
		t.Fatalf("proxy got %s %s, want CONNECT", s.method, s.host)
	}
	if s.proxyAuth != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:secret")) {
		t.Fatalf("Proxy-Authorization %q", s.proxyAuth)
	}
	if s.auth != "" {
		t.Fatal("the proxy saw the token")
	}
	if targetAuth != "Bearer "+testKey {
		t.Fatalf("target got Authorization %q", targetAuth)
	}
}

func TestProxyErrorsHideCredentials(t *testing.T) {
	proxyURL := &url.URL{Scheme: "http", User: url.UserPassword("user", "hunter2pass"), Host: closedAddr(t)}
	for _, base := range []string{"http://localhost:9", "https://api.polyzon.org"} {
		c, err := NewClient(WithAPIKey(testKey), WithBaseURL(base), WithProxy(proxyURL), WithMaxRetries(0))
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Me(context.Background())
		if !errors.Is(err, ErrConnection) {
			t.Fatalf("want ErrConnection, got %v", err)
		}
		if strings.Contains(err.Error(), "hunter2pass") || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("error leaks credentials: %v", err)
		}
	}

	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer refusing.Close()
	proxyURL, _ = url.Parse(refusing.URL)
	proxyURL.User = url.UserPassword("user", "hunter2pass")
	for _, base := range []string{"http://localhost:9", "https://api.polyzon.org"} {
		c, err := NewClient(WithAPIKey(testKey), WithBaseURL(base), WithProxy(proxyURL), WithMaxRetries(0))
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Me(context.Background())
		if err == nil || strings.Contains(err.Error(), "hunter2pass") {
			t.Fatalf("want an error without credentials, got %v", err)
		}
		c.CloseIdleConnections()
	}
}
