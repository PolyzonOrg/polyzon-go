package polyzon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func ptr(n int64) *int64 { return &n }

func TestSearch(t *testing.T) {
	var gotPath, gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		reply(w, 200, fixture(t, "search.json"), "X-Request-ID", "req-7",
			"X-Ratelimit-Limit", "200", "X-Ratelimit-Remaining", "163", "X-Ratelimit-Reset", "1791590400")
	})
	page, err := c.Search(context.Background(), SearchParams{Query: "дракон"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/search" || gotQuery != "q=%D0%B4%D1%80%D0%B0%D0%BA%D0%BE%D0%BD" {
		t.Fatalf("request %s?%s", gotPath, gotQuery)
	}
	zero := 0.0
	want := &SearchPage{
		Query: "дракон", Total: 1873, TotalRelation: TotalEq, Page: 1, Size: 20, HasMore: true,
		Items: []SearchItem{{
			Source: SourceThingiverse, ID: "tv-1234567", Title: "Дракон с подвижными сочленениями (пример)",
			URL: "https://www.thingiverse.com/thing:1234567", Thumbnail: "https://cdn.thingiverse.com/assets/dragon.jpg",
			Creator: "example_maker", License: LicenseCCBYNC,
			Price:   &Price{Type: PriceFree, Value: &zero, Display: "Бесплатно"},
			Formats: []string{"stl"}, Tags: []string{"дракон", "игрушка"},
			Stats:       ItemStats{Likes: ptr(51234), Comments: ptr(870), Makes: ptr(3120), Collects: ptr(61000)},
			PublishedAt: "2021-01-17T10:21:00Z",
		}},
		Meta: Meta{RequestID: "req-7", RateLimit: &RateLimit{Limit: 200, Remaining: 163, ResetAt: 1791590400}},
	}
	if !reflect.DeepEqual(page, want) {
		t.Fatalf("got  %+v\nwant %+v", page, want)
	}

	// Meta is not part of the JSON shape; the rest round-trips to the wire names.
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "req-7") || !strings.Contains(string(encoded), `"totalRelation":"eq"`) {
		t.Fatalf("JSON %s", encoded)
	}
}

func TestSearchQueryEncoding(t *testing.T) {
	var got string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		reply(w, 200, fixture(t, "search.json"))
	})
	tests := []struct {
		params SearchParams
		want   string
	}{
		{SearchParams{Query: "dragon"}, "q=dragon"},
		{SearchParams{}, ""},
		{SearchParams{Query: "a&b=c #1+2"}, "q=a%26b%3Dc+%231%2B2"},
		{
			SearchParams{Query: "Дракон ёж", Page: 3, Size: 50, Sort: SortNewest, Free: true,
				Sources: []string{SourceThingiverse, SourcePrintables}, Formats: []string{"stl", "3mf"},
				Licenses: []string{LicenseCC0, LicenseCCBY}, Lang: LangEN},
			"formats=stl%2C3mf&free=1&lang=en&license=cc0%2Ccc-by&page=3&q=%D0%94%D1%80%D0%B0%D0%BA%D0%BE%D0%BD+%D1%91%D0%B6" +
				"&size=50&sort=newest&sources=thingiverse%2Cprintables",
		},
		{SearchParams{Query: "x", Free: false, Sources: []string{}, Sort: ""}, "q=x"},
	}
	for _, tt := range tests {
		if _, err := c.Search(context.Background(), tt.params); err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("query %q, want %q", got, tt.want)
		}
	}
}

func TestGetModel(t *testing.T) {
	var gotPath, gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		reply(w, 200, fixture(t, "model.json"), "X-Request-ID", "req-9")
	})
	model, err := c.GetModel(context.Background(), SourceThingiverse, "tv-1234567", WithLang(LangEN))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models/thingiverse/tv-1234567" || gotQuery != "lang=en" {
		t.Fatalf("request %s?%s", gotPath, gotQuery)
	}
	zero := 0.0
	want := &Model{
		Source: SourceThingiverse, ID: "tv-1234567", Title: "Дракон с подвижными сочленениями (пример)",
		URL: "https://www.thingiverse.com/thing:1234567", Description: "Дракон печатается целиком, без поддержек…",
		Thumbnail: "https://cdn.thingiverse.com/assets/dragon.jpg", Images: []string{"https://cdn.thingiverse.com/assets/dragon.jpg"},
		Creator: "example_maker", CreatorURL: "https://www.thingiverse.com/example_maker",
		License: &License{ID: LicenseCCBYNC, Raw: "Creative Commons - Attribution - Non-Commercial"},
		Price:   &Price{Type: PriceFree, Value: &zero, Display: "Бесплатно"},
		Formats: []string{"stl"}, Tags: []string{"дракон", "игрушка"}, Category: "Toys & Games",
		Stats:       ModelStats{Likes: ptr(51234), Comments: ptr(870), Makes: ptr(3120), Collects: ptr(61000), Downloads: ptr(410000), Views: ptr(990000)},
		PublishedAt: "2021-01-17T10:21:00Z", UpdatedAt: "2021-02-02T08:00:00Z",
		Copies: []ModelRef{{Source: SourcePrintables, ID: "pr-123456"}},
		Meta:   Meta{RequestID: "req-9"},
	}
	if !reflect.DeepEqual(model, want) {
		t.Fatalf("got  %+v\nwant %+v", model, want)
	}

	if _, err := c.GetModel(context.Background(), "nih3d", "3DPX-000001_v1.0"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models/nih3d/3DPX-000001_v1.0" || gotQuery != "" {
		t.Fatalf("request %s?%s", gotPath, gotQuery)
	}
}

func TestGetModelValidation(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	tests := []struct{ source, id string }{
		{"", "tv-1"},
		{"Thingiverse", "tv-1"},
		{"thing-verse", "tv-1"},
		{strings.Repeat("a", 33), "tv-1"},
		{"thingiverse", ""},
		{"thingiverse", "."},
		{"thingiverse", ".."},
		{"thingiverse", "../../v1/me"},
		{"thingiverse", "a/b"},
		{"thingiverse", "a b"},
		{"thingiverse", "a%2Fb"},
		{"thingiverse", "a?b"},
		{"thingiverse", "a#b"},
		{"thingiverse", "модель"},
		{"thingiverse", "tv-1\n"},
		{"thingiverse", strings.Repeat("a", 256)},
	}
	for _, tt := range tests {
		_, err := c.GetModel(context.Background(), tt.source, tt.id)
		if err == nil {
			t.Errorf("GetModel(%q, %q): want error", tt.source, tt.id)
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConnection) {
			t.Errorf("GetModel(%q, %q): an argument error is not an API error: %v", tt.source, tt.id, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("%d requests sent for invalid arguments", calls.Load())
	}
	if _, err := modelPath(strings.Repeat("a", 32), strings.Repeat("A", 255)); err != nil {
		t.Fatalf("longest valid key rejected: %v", err)
	}
	if _, err := modelPath("a", "..."); err != nil {
		t.Fatalf("three dots are a valid id: %v", err)
	}
}

func TestMe(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		reply(w, 200, fixture(t, "me.json"))
	})
	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := &Me{
		Account: Account{ID: "0f8c2a4e-…", DisplayName: "Алексей", CreatedAt: "2026-03-14T10:00:00.000Z"},
		Token:   TokenInfo{ID: "7d0e2f8a-…", Name: "Телеграм-бот", Prefix: "pzk_Q7xR2mVb", CreatedAt: "2026-10-09T08:15:00.000Z"},
	}
	if gotPath != "/v1/me" || !reflect.DeepEqual(me, want) {
		t.Fatalf("%s: got %+v", gotPath, me)
	}
}

func TestLimits(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		reply(w, 200, fixture(t, "limits.json"))
	})
	limits, err := c.Limits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := &Limits{
		PerMinute: QuotaWindow{Limit: 20, Used: 1, Remaining: 19, ResetAt: 1791579180},
		Daily:     QuotaWindow{Limit: 200, Used: 37, Remaining: 163, ResetAt: 1791590400},
	}
	if gotPath != "/v1/limits" || !reflect.DeepEqual(limits, want) {
		t.Fatalf("%s: got %+v", gotPath, limits)
	}
}

func TestMetaParsing(t *testing.T) {
	tests := []struct {
		headers []string
		want    Meta
	}{
		{nil, Meta{}},
		{[]string{"X-Request-ID", "abc"}, Meta{RequestID: "abc"}},
		{[]string{"x-ratelimit-limit", "200", "x-ratelimit-remaining", "0", "x-ratelimit-reset", "1791590400"},
			Meta{RateLimit: &RateLimit{Limit: 200, Remaining: 0, ResetAt: 1791590400}}},
		{[]string{"x-ratelimit-limit", "200", "x-ratelimit-remaining", "5"}, Meta{}},
		{[]string{"x-ratelimit-limit", "lots", "x-ratelimit-remaining", "5", "x-ratelimit-reset", "1"}, Meta{}},
	}
	for _, tt := range tests {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, 200, fixture(t, "limits.json"), tt.headers...)
		})
		limits, err := c.Limits(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(limits.Meta, tt.want) {
			t.Errorf("headers %v: meta %+v", tt.headers, limits.Meta)
		}
	}
}

func TestLenientParsing(t *testing.T) {
	body := `{
		"query": 5, "correctedQuery": "дракоН", "didYouMean": {"query": "x"},
		"total": 1873.9, "totalRelation": "approx", "page": "1", "size": 20.0,
		"hasMore": "yes", "partial": true, "future": {"nested": [1, 2]},
		"items": [
			7, null, "item",
			{"source": "newsource", "id": "n-1", "title": ["t"], "url": null,
			 "price": {"type": "subscription", "value": "12", "currency": 1, "display": null},
			 "formats": ["stl", 3, null, "obj"], "tags": "not-a-list",
			 "stats": {"likes": 12.0, "comments": 7.9, "makes": -3, "collects": 1e30, "extra": 1},
			 "publishedAt": 20210117},
			{"source": "thingiverse", "id": "tv-2", "price": [], "stats": [], "license": {"id": "cc0"}}
		]
	}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, []byte(body)) })
	page, err := c.Search(context.Background(), SearchParams{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Query != "" || page.CorrectedQuery != "дракоН" || page.DidYouMean != "" || page.Total != 1873 ||
		page.TotalRelation != "approx" || page.Page != 0 || page.Size != 20 || page.HasMore || !page.Partial {
		t.Fatalf("page %+v", page)
	}
	if len(page.Items) != 2 {
		t.Fatalf("non-object items must be skipped, got %d", len(page.Items))
	}
	item := page.Items[0]
	if item.Source != "newsource" || item.Title != "" || item.URL != "" || item.PublishedAt != "" ||
		!reflect.DeepEqual(item.Formats, []string{"stl", "obj"}) || len(item.Tags) != 0 {
		t.Fatalf("item %+v", item)
	}
	if item.Price == nil || item.Price.Type != "subscription" || item.Price.Value != nil || item.Price.Currency != "" {
		t.Fatalf("price %+v", item.Price)
	}
	wantStats := ItemStats{Likes: ptr(12), Comments: ptr(7), Makes: ptr(-3)}
	if !reflect.DeepEqual(item.Stats, wantStats) {
		t.Fatalf("stats %+v", item.Stats)
	}
	second := page.Items[1]
	if second.Price != nil || second.License != "" || second.Stats != (ItemStats{}) {
		t.Fatalf("second %+v", second)
	}

	model := parseModel(mustObject(t, `{"license": {"id": 5, "raw": null}, "copies": [1, {"source": "a"}, {"source": "b", "id": "c"}],
		"images": null, "stats": {"downloads": 9007199254740993, "views": "1"}}`))
	if model.License == nil || model.License.ID != "" || len(model.Images) != 0 {
		t.Fatalf("model %+v", model)
	}
	if !reflect.DeepEqual(model.Copies, []ModelRef{{Source: "a"}, {Source: "b", ID: "c"}}) {
		t.Fatalf("copies %+v", model.Copies)
	}
	if model.Stats.Downloads == nil || *model.Stats.Downloads != 9007199254740993 || model.Stats.Views != nil {
		t.Fatalf("64-bit counts must keep precision: %+v", model.Stats)
	}

	limits := parseLimits(mustObject(t, `{"perMinute": {"limit": 20.5, "used": null}, "daily": 3}`))
	if limits.PerMinute != (QuotaWindow{Limit: 20}) || limits.Daily != (QuotaWindow{}) {
		t.Fatalf("limits %+v", limits)
	}
	me := parseMe(mustObject(t, `{"account": {"displayName": null}, "token": []}`))
	if *me != (Me{}) {
		t.Fatalf("me %+v", me)
	}
}

func mustObject(t *testing.T, s string) object {
	t.Helper()
	o, err := parseObject([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// pagedServer serves `pages` pages of `size` items with hasMore on all but the last.
func pagedServer(t *testing.T, pages, size int, requested *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		*requested = append(*requested, q.Get("page"))
		n, _ := strconv.Atoi(q.Get("page"))
		items := make([]map[string]any, 0, size)
		for i := range size {
			items = append(items, map[string]any{"source": "thingiverse", "id": fmt.Sprintf("tv-%d-%d", n, i), "title": "t", "url": "u"})
		}
		body, _ := json.Marshal(map[string]any{
			"query": q.Get("q"), "total": pages * size, "page": n, "size": size, "hasMore": n < pages, "items": items,
		})
		reply(w, 200, body)
	}
}

func TestSearchAll(t *testing.T) {
	t.Run("walks every page", func(t *testing.T) {
		var requested []string
		c, _ := newTestClient(t, pagedServer(t, 3, 2, &requested))
		var ids []string
		for item, err := range c.SearchAll(context.Background(), SearchParams{Query: "x", Size: 2}) {
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, item.ID)
		}
		want := []string{"tv-1-0", "tv-1-1", "tv-2-0", "tv-2-1", "tv-3-0", "tv-3-1"}
		if !reflect.DeepEqual(ids, want) || !reflect.DeepEqual(requested, []string{"1", "2", "3"}) {
			t.Fatalf("ids %v, pages %v", ids, requested)
		}
	})
	t.Run("starts at params.Page", func(t *testing.T) {
		var requested []string
		c, _ := newTestClient(t, pagedServer(t, 3, 1, &requested))
		n := 0
		for _, err := range c.SearchAll(context.Background(), SearchParams{Query: "x", Page: 2}) {
			if err != nil {
				t.Fatal(err)
			}
			n++
		}
		if n != 2 || !reflect.DeepEqual(requested, []string{"2", "3"}) {
			t.Fatalf("%d items, pages %v", n, requested)
		}
	})
	t.Run("the iterator is reusable", func(t *testing.T) {
		var requested []string
		c, _ := newTestClient(t, pagedServer(t, 2, 1, &requested))
		seq := c.SearchAll(context.Background(), SearchParams{Query: "x"})
		for range 2 {
			for _, err := range seq {
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if !reflect.DeepEqual(requested, []string{"1", "2", "1", "2"}) {
			t.Fatalf("pages %v", requested)
		}
	})
	t.Run("break stops fetching", func(t *testing.T) {
		var requested []string
		c, _ := newTestClient(t, pagedServer(t, 5, 2, &requested))
		for range c.SearchAll(context.Background(), SearchParams{Query: "x"}) {
			break
		}
		if len(requested) != 1 {
			t.Fatalf("pages %v", requested)
		}
	})
	t.Run("stops on an empty page", func(t *testing.T) {
		var calls atomic.Int32
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			reply(w, 200, []byte(`{"total":100,"page":1,"size":20,"hasMore":true,"items":[]}`))
		})
		for _, err := range c.SearchAll(context.Background(), SearchParams{Query: "x"}) {
			t.Fatalf("no items expected, got err %v", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("%d calls", calls.Load())
		}
	})
	t.Run("stops after page 100, the deepest the API serves", func(t *testing.T) {
		// size 20 < 24 with 5000 results: hasMore is still true on page 100
		// (100 × 20 < 2400), but the API rejects page 101 with 400 bad_request.
		var requested []string
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			requested = append(requested, strconv.Itoa(page))
			if page > 100 {
				reply(w, 400, problem(400, CodeBadRequest, "page: от 1 до 100"))
				return
			}
			reply(w, 200, []byte(fmt.Sprintf(`{"total":5000,"page":%d,"size":20,"hasMore":%t,"items":[{"id":"tv-%d"}]}`,
				page, page*20 < 2400, page)))
		})
		n := 0
		for _, err := range c.SearchAll(context.Background(), SearchParams{Query: "x", Size: 20, Page: 98}) {
			if err != nil {
				t.Fatalf("SearchAll went past page 100: %v", err)
			}
			n++
		}
		if n != 3 || !reflect.DeepEqual(requested, []string{"98", "99", "100"}) {
			t.Fatalf("%d items, pages %v", n, requested)
		}
	})
	t.Run("yields the error and stops", func(t *testing.T) {
		var calls atomic.Int32
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				reply(w, 200, []byte(`{"total":10,"page":1,"size":1,"hasMore":true,"items":[{"id":"a"}]}`))
				return
			}
			reply(w, 429, problem(429, CodeQuotaExceeded, "квота"), "Retry-After", "3600")
		})
		var items, errs int
		for _, err := range c.SearchAll(context.Background(), SearchParams{Query: "x"}) {
			if err != nil {
				errs++
				if !errors.Is(err, ErrQuotaExceeded) {
					t.Fatalf("got %v", err)
				}
				continue
			}
			items++
		}
		if items != 1 || errs != 1 || calls.Load() != 2 {
			t.Fatalf("%d items, %d errors, %d calls", items, errs, calls.Load())
		}
	})
}

func TestGetModels(t *testing.T) {
	t.Run("input order and bounded concurrency", func(t *testing.T) {
		var inFlight, peak atomic.Int32
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			id := strings.TrimPrefix(r.URL.Path, "/v1/models/thingiverse/")
			i, _ := strconv.Atoi(strings.TrimPrefix(id, "tv-"))
			time.Sleep(time.Duration(10-i%10) * 20 * time.Millisecond) // later refs finish first; long enough to overlap under load
			if r.URL.Query().Get("lang") != "en" {
				t.Error("lang option not passed")
			}
			if id == "tv-5" {
				reply(w, 404, problem(404, CodeNotFound, "Модель не найдена."))
				return
			}
			reply(w, 200, []byte(`{"source":"thingiverse","id":"`+id+`","title":"t","url":"u"}`))
		})
		refs := make([]ModelRef, 12)
		for i := range refs {
			refs[i] = ModelRef{Source: SourceThingiverse, ID: fmt.Sprintf("tv-%d", i)}
		}
		refs[7].ID = "../bad"
		results, err := c.GetModels(context.Background(), refs, WithConcurrency(3), WithLang(LangEN))
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != len(refs) {
			t.Fatalf("%d results", len(results))
		}
		for i, res := range results {
			if res.Ref != refs[i] {
				t.Fatalf("result %d is for %v", i, res.Ref)
			}
			switch i {
			case 5:
				if !errors.Is(res.Err, ErrNotFound) || res.Model != nil {
					t.Fatalf("result 5: %+v", res)
				}
			case 7:
				if res.Err == nil || res.Model != nil {
					t.Fatalf("result 7: %+v", res)
				}
			default:
				if res.Err != nil || res.Model.ID != refs[i].ID {
					t.Fatalf("result %d: %+v", i, res)
				}
			}
		}
		if p := peak.Load(); p > 3 || p < 2 {
			t.Fatalf("peak concurrency %d, want 2..3", p)
		}
	})
	for _, tt := range []struct {
		status int
		code   string
		want   error
	}{
		{401, CodeInvalidToken, ErrAuthentication},
		{403, CodeAccountActionRequired, ErrPermissionDenied},
		{429, CodeQuotaExceeded, ErrQuotaExceeded},
	} {
		t.Run("short-circuit on "+tt.code, func(t *testing.T) {
			var calls atomic.Int32
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				reply(w, tt.status, problem(tt.status, tt.code, "stop"), "Retry-After", "3600")
			})
			refs := []ModelRef{{"thingiverse", "tv-1"}, {"thingiverse", "tv-2"}, {"thingiverse", "tv-3"}}
			results, err := c.GetModels(context.Background(), refs, WithConcurrency(1))
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("%d requests after a %s", calls.Load(), tt.code)
			}
			for i, res := range results {
				if !errors.Is(res.Err, tt.want) || res.Err != results[0].Err {
					t.Fatalf("result %d: %v", i, res.Err)
				}
			}
		})
	}
	t.Run("not found does not short-circuit", func(t *testing.T) {
		var calls atomic.Int32
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			reply(w, 404, problem(404, CodeNotFound, "нет"))
		})
		refs := []ModelRef{{"thingiverse", "tv-1"}, {"thingiverse", "tv-2"}}
		if _, err := c.GetModels(context.Background(), refs, WithConcurrency(1)); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 2 {
			t.Fatalf("%d calls", calls.Load())
		}
	})
	t.Run("invalid concurrency", func(t *testing.T) {
		c, _ := newTestClient(t, nil)
		for _, n := range []int{0, -1} {
			if _, err := c.GetModels(context.Background(), nil, WithConcurrency(n)); err == nil {
				t.Fatalf("concurrency %d accepted", n)
			}
		}
	})
	t.Run("no refs", func(t *testing.T) {
		c, _ := newTestClient(t, nil)
		results, err := c.GetModels(context.Background(), nil)
		if err != nil || len(results) != 0 {
			t.Fatalf("%v %v", results, err)
		}
	})
}
