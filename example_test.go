package polyzon_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PolyzonOrg/polyzon-go"
)

func ExampleNewClient() {
	// The token comes from POLYZON_API_KEY; polyzon.WithAPIKey passes it explicitly.
	client, err := polyzon.NewClient(
		polyzon.WithTimeout(10*time.Second),
		polyzon.WithMaxRetries(3),
		polyzon.WithUserAgent("my-bot/1.0"),
	)
	if err != nil {
		log.Fatal(err)
	}
	me, err := client.Me(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(me.Account.DisplayName, me.Token.Prefix)
	// Output: Алексей pzk_Q7xR2mVb
}

func ExampleClient_Search() {
	client, err := polyzon.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	page, err := client.Search(context.Background(), polyzon.SearchParams{
		Query:   "дракон",
		Sort:    polyzon.SortPopular,
		Free:    true,
		Sources: []string{polyzon.SourceThingiverse, polyzon.SourcePrintables},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("total:", page.Total)
	for _, item := range page.Items {
		fmt.Printf("%s/%s %s (%s)\n", item.Source, item.ID, item.Title, item.License)
	}
	fmt.Println("daily requests left:", page.Meta.RateLimit.Remaining)
	// Output:
	// total: 1873
	// thingiverse/tv-1234567 Дракон с подвижными сочленениями (пример) (cc-by-nc)
	// daily requests left: 163
}

func ExampleClient_SearchAll() {
	client, err := polyzon.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	// Each page costs one request of the daily quota; break to stop early.
	for item, err := range client.SearchAll(context.Background(), polyzon.SearchParams{Query: "дракон", Size: 50}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(item.Title)
	}
	// Output: Дракон с подвижными сочленениями (пример)
}

func ExampleClient_GetModel() {
	client, err := polyzon.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	model, err := client.GetModel(context.Background(), polyzon.SourceThingiverse, "tv-1234567",
		polyzon.WithLang(polyzon.LangRU))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(model.Title)
	fmt.Println("license:", model.License.ID)
	if model.Stats.Downloads != nil {
		fmt.Println("downloads:", *model.Stats.Downloads)
	}
	for _, other := range model.Copies {
		fmt.Println("also on", other.Source, other.ID)
	}
	// Output:
	// Дракон с подвижными сочленениями (пример)
	// license: cc-by-nc
	// downloads: 410000
	// also on printables pr-123456
}

func ExampleClient_GetModels() {
	client, err := polyzon.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	refs := []polyzon.ModelRef{
		{Source: polyzon.SourceThingiverse, ID: "tv-1234567"},
		{Source: polyzon.SourceThingiverse, ID: "tv-404"},
	}
	results, err := client.GetModels(context.Background(), refs, polyzon.WithConcurrency(4))
	if err != nil {
		log.Fatal(err)
	}
	for _, res := range results { // in the order of refs
		if res.Err != nil {
			fmt.Println(res.Ref.ID, "not found:", errors.Is(res.Err, polyzon.ErrNotFound))
			continue
		}
		fmt.Println(res.Ref.ID, res.Model.Title)
	}
	// Output:
	// tv-1234567 Дракон с подвижными сочленениями (пример)
	// tv-404 not found: true
}

func ExampleClient_Limits() {
	client, err := polyzon.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	limits, err := client.Limits(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d of %d daily requests left, resets at %s\n", limits.Daily.Remaining, limits.Daily.Limit,
		time.Unix(limits.Daily.ResetAt, 0).UTC().Format(time.RFC3339))
	// Output: 163 of 200 daily requests left, resets at 2026-10-10T00:00:00Z
}

func ExampleAPIError() {
	client, err := polyzon.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	_, err = client.GetModel(context.Background(), polyzon.SourceThingiverse, "tv-404")
	switch {
	case errors.Is(err, polyzon.ErrNotFound):
		fmt.Println("no such model")
	case errors.Is(err, polyzon.ErrGone):
		fmt.Println("removed on the right holder's request: delete it")
	case errors.Is(err, polyzon.ErrQuotaExceeded):
		fmt.Println("daily quota spent until 00:00 UTC")
	case errors.Is(err, polyzon.ErrConnection):
		fmt.Println("network trouble:", err)
	}
	var apiErr *polyzon.APIError
	if errors.As(err, &apiErr) {
		fmt.Println(apiErr.StatusCode, apiErr.Code, apiErr.RequestID)
	}
	// Output:
	// no such model
	// 404 not_found example
}

func ExampleWithProxy() {
	// Credentials in the URL become Basic proxy authentication. Requests to the
	// https API are tunnelled with CONNECT, so the proxy never sees the token.
	proxyURL, err := url.Parse("http://user:password@proxy.internal:3128")
	if err != nil {
		log.Fatal(err)
	}
	client, err := polyzon.NewClient(polyzon.WithProxy(proxyURL))
	if err != nil {
		log.Fatal(err)
	}
	_ = client
}

func ExampleWithHTTPClient() {
	// Trust a corporate CA in addition to the system roots. TLS verification
	// itself cannot be turned off.
	pool, err := x509.SystemCertPool()
	if err != nil {
		log.Fatal(err)
	}
	pem, err := os.ReadFile("corporate-ca.pem")
	if err != nil {
		log.Fatal(err)
	}
	pool.AppendCertsFromPEM(pem)
	client, err := polyzon.NewClient(polyzon.WithHTTPClient(&http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}))
	if err != nil {
		log.Fatal(err)
	}
	_ = client
}

// TestMain serves a stand-in for the API, answering the documented examples, so
// that the examples above run offline and their output is checked.
func TestMain(m *testing.M) {
	server := httptest.NewServer(http.HandlerFunc(fakeAPI))
	_ = os.Setenv("POLYZON_API_KEY", "pzk_"+strings.Repeat("A", 43))
	_ = os.Setenv("POLYZON_BASE_URL", server.URL)
	code := m.Run()
	server.Close()
	os.Exit(code)
}

func fakeAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Request-ID", "example")
	w.Header().Set("X-Ratelimit-Limit", "200")
	w.Header().Set("X-Ratelimit-Remaining", "163")
	w.Header().Set("X-Ratelimit-Reset", "1791590400")
	if r.URL.Path == "/v1/search" && r.URL.Query().Get("page") > "1" {
		_, _ = w.Write([]byte(`{"query":"дракон","total":1873,"totalRelation":"eq","page":2,"size":50,"hasMore":false,"items":[]}`))
		return
	}
	name := map[string]string{
		"/v1/search":                        "search.json",
		"/v1/models/thingiverse/tv-1234567": "model.json",
		"/v1/me":                            "me.json",
		"/v1/limits":                        "limits.json",
	}[r.URL.Path]
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if name == "" || err != nil {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"not_found","detail":"Модель не найдена."}`))
		return
	}
	_, _ = w.Write(body)
}
