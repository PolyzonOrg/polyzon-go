package polyzon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
)

// Meta is the metadata of a successful answer. It is not part of the JSON body,
// so it is excluded from JSON encoding of the result types.
type Meta struct {
	// RequestID is the X-Request-ID response header, or "".
	RequestID string
	// RateLimit is the daily quota window from the x-ratelimit-* headers, or nil
	// when the answer did not carry them.
	RateLimit *RateLimit
}

// RateLimit is the daily quota window reported in the x-ratelimit-* headers.
type RateLimit struct {
	Limit     int64 // requests per UTC day
	Remaining int64 // requests left today
	ResetAt   int64 // next reset (00:00 UTC), Unix seconds
}

// SearchPage is one page of search results.
type SearchPage struct {
	// Query is the query as the server understood it.
	Query string `json:"query"`
	// CorrectedQuery is the spelling-corrected query actually searched, or "".
	CorrectedQuery string `json:"correctedQuery"`
	// DidYouMean is a suggested alternative query, or "".
	DidYouMean string `json:"didYouMean"`
	// Total is the number of matching models (see TotalRelation).
	Total int64 `json:"total"`
	// TotalRelation is TotalEq when Total is exact, TotalGte when it is a lower bound.
	TotalRelation string `json:"totalRelation"`
	Page          int    `json:"page"`
	Size          int    `json:"size"`
	// HasMore reports whether a next page exists within the 2400-result window.
	HasMore bool `json:"hasMore"`
	// Partial reports that part of the catalogue did not answer in time, so the
	// results may be incomplete.
	Partial bool         `json:"partial"`
	Items   []SearchItem `json:"items"`
	Meta    Meta         `json:"-"`
}

// SearchItem is a model in search results. Optional text fields are "" when the
// source does not provide them.
type SearchItem struct {
	Source    string `json:"source"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	URL       string `json:"url"` // the model page on the source site
	Thumbnail string `json:"thumbnail"`
	Creator   string `json:"creator"`
	// License is a license id (the License* constants), or "".
	License string `json:"license"`
	// Price is nil when unknown.
	Price   *Price    `json:"price"`
	Formats []string  `json:"formats"`
	Tags    []string  `json:"tags"`
	Stats   ItemStats `json:"stats"`
	// PublishedAt is an ISO-8601 timestamp as reported by the source, or "".
	PublishedAt string `json:"publishedAt"`
}

// ItemStats are the counters of a search item; nil means the source does not
// report the counter.
type ItemStats struct {
	Likes    *int64 `json:"likes"`
	Comments *int64 `json:"comments"`
	Makes    *int64 `json:"makes"`
	// Collects counts saves to collections, or downloads where the source does
	// not count saves.
	Collects *int64 `json:"collects"`
}

// ModelStats are the counters of a model card; nil means unknown.
type ModelStats struct {
	Likes     *int64 `json:"likes"`
	Comments  *int64 `json:"comments"`
	Makes     *int64 `json:"makes"`
	Collects  *int64 `json:"collects"`
	Downloads *int64 `json:"downloads"`
	Views     *int64 `json:"views"`
}

// Price is the price of a model.
type Price struct {
	// Type is PriceFree, PricePaid or PriceOpen (pay what you want).
	Type string `json:"type"`
	// Value is the amount, or nil when unknown.
	Value *float64 `json:"value"`
	// Currency is an ISO 4217 code, or "".
	Currency string `json:"currency"`
	// Display is the price formatted for people.
	Display string `json:"display"`
}

// License is the license of a model card.
type License struct {
	ID  string `json:"id"`  // a License* constant or another id
	Raw string `json:"raw"` // the license text as named by the source, or ""
}

// ModelRef identifies a model: the source and the model id on it, as in
// polyzon.org/model/{source}/{id}.
type ModelRef struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

// Model is a model card. Optional text fields are "" when unknown.
type Model struct {
	Source      string     `json:"source"`
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	Description string     `json:"description"`
	Thumbnail   string     `json:"thumbnail"`
	Images      []string   `json:"images"` // URLs on the source's CDN
	Creator     string     `json:"creator"`
	CreatorURL  string     `json:"creatorUrl"`
	License     *License   `json:"license"` // nil when unknown
	Price       *Price     `json:"price"`   // nil when unknown
	Formats     []string   `json:"formats"`
	Tags        []string   `json:"tags"`
	Category    string     `json:"category"`
	Stats       ModelStats `json:"stats"`
	PublishedAt string     `json:"publishedAt"` // ISO-8601 or ""
	UpdatedAt   string     `json:"updatedAt"`   // ISO-8601 or ""
	// Copies are the same model on other sources.
	Copies []ModelRef `json:"copies"`
	Meta   Meta       `json:"-"`
}

// Me describes the account and the token used for the request.
type Me struct {
	Account Account   `json:"account"`
	Token   TokenInfo `json:"token"`
	Meta    Meta      `json:"-"`
}

// Account is the Polyzon account owning the token.
type Account struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"` // "" when not set
	CreatedAt   string `json:"createdAt"`   // ISO-8601
}

// TokenInfo describes an API token (never the secret itself).
type TokenInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`    // the first characters of the token, e.g. "pzk_Q7xR2mVb"
	CreatedAt string `json:"createdAt"` // ISO-8601
}

// Limits are the account's quota windows. Quotas are per account, shared by all
// its tokens.
type Limits struct {
	PerMinute QuotaWindow `json:"perMinute"` // every method
	Daily     QuotaWindow `json:"daily"`     // search and model cards, UTC day
	Meta      Meta        `json:"-"`
}

// QuotaWindow is one quota window.
type QuotaWindow struct {
	Limit     int64 `json:"limit"`
	Used      int64 `json:"used"`
	Remaining int64 `json:"remaining"`
	ResetAt   int64 `json:"resetAt"` // start of the next window, Unix seconds
}

// object is a decoded JSON object read leniently, like the server's present.ts:
// a missing or wrong-typed field reads as its zero value, never as an error.
type object map[string]any

var errNotObject = errors.New("body is not a JSON object")

// parseObject decodes a whole body that must be one JSON object. Numbers stay
// json.Number so that 64-bit integers keep their precision.
func parseObject(body []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, errNotObject
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errNotObject // trailing data
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, errNotObject
	}
	return m, nil
}

func (o object) obj(key string) object {
	m, _ := o[key].(map[string]any)
	return m
}

func (o object) str(key string) string {
	s, _ := o[key].(string)
	return s
}

func (o object) boolean(key string) bool {
	b, _ := o[key].(bool)
	return b
}

// strs keeps only the string elements of an array.
func (o object) strs(key string) []string {
	list, _ := o[key].([]any)
	out := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// count reads an integer; floats are truncated (12.7 → 12), values outside the
// int64 range or of another type are nil.
func (o object) count(key string) *int64 {
	n, ok := o[key].(json.Number)
	if !ok {
		return nil
	}
	if i, err := n.Int64(); err == nil {
		return &i
	}
	f, err := n.Float64()
	if err != nil || f >= math.MaxInt64 || f < math.MinInt64 {
		return nil
	}
	i := int64(f)
	return &i
}

func (o object) integer(key string) int64 {
	if p := o.count(key); p != nil {
		return *p
	}
	return 0
}

func (o object) float(key string) *float64 {
	n, ok := o[key].(json.Number)
	if !ok {
		return nil
	}
	f, err := n.Float64()
	if err != nil {
		return nil
	}
	return &f
}

func parsePrice(o object) *Price {
	if o == nil {
		return nil
	}
	return &Price{Type: o.str("type"), Value: o.float("value"), Currency: o.str("currency"), Display: o.str("display")}
}

func parseSearchPage(o object) *SearchPage {
	list, _ := o["items"].([]any)
	items := make([]SearchItem, 0, len(list))
	for _, v := range list {
		if m, ok := v.(map[string]any); ok {
			items = append(items, parseSearchItem(m))
		}
	}
	return &SearchPage{
		Query:          o.str("query"),
		CorrectedQuery: o.str("correctedQuery"),
		DidYouMean:     o.str("didYouMean"),
		Total:          o.integer("total"),
		TotalRelation:  o.str("totalRelation"),
		Page:           int(o.integer("page")),
		Size:           int(o.integer("size")),
		HasMore:        o.boolean("hasMore"),
		Partial:        o.boolean("partial"),
		Items:          items,
	}
}

func parseSearchItem(o object) SearchItem {
	stats := o.obj("stats")
	return SearchItem{
		Source:      o.str("source"),
		ID:          o.str("id"),
		Title:       o.str("title"),
		URL:         o.str("url"),
		Thumbnail:   o.str("thumbnail"),
		Creator:     o.str("creator"),
		License:     o.str("license"),
		Price:       parsePrice(o.obj("price")),
		Formats:     o.strs("formats"),
		Tags:        o.strs("tags"),
		Stats:       ItemStats{Likes: stats.count("likes"), Comments: stats.count("comments"), Makes: stats.count("makes"), Collects: stats.count("collects")},
		PublishedAt: o.str("publishedAt"),
	}
}

func parseModel(o object) *Model {
	stats := o.obj("stats")
	var license *License
	if l := o.obj("license"); l != nil {
		license = &License{ID: l.str("id"), Raw: l.str("raw")}
	}
	list, _ := o["copies"].([]any)
	copies := make([]ModelRef, 0, len(list))
	for _, v := range list {
		if m, ok := v.(map[string]any); ok {
			copies = append(copies, ModelRef{Source: object(m).str("source"), ID: object(m).str("id")})
		}
	}
	return &Model{
		Source:      o.str("source"),
		ID:          o.str("id"),
		Title:       o.str("title"),
		URL:         o.str("url"),
		Description: o.str("description"),
		Thumbnail:   o.str("thumbnail"),
		Images:      o.strs("images"),
		Creator:     o.str("creator"),
		CreatorURL:  o.str("creatorUrl"),
		License:     license,
		Price:       parsePrice(o.obj("price")),
		Formats:     o.strs("formats"),
		Tags:        o.strs("tags"),
		Category:    o.str("category"),
		Stats: ModelStats{
			Likes: stats.count("likes"), Comments: stats.count("comments"), Makes: stats.count("makes"),
			Collects: stats.count("collects"), Downloads: stats.count("downloads"), Views: stats.count("views"),
		},
		PublishedAt: o.str("publishedAt"),
		UpdatedAt:   o.str("updatedAt"),
		Copies:      copies,
	}
}

func parseMe(o object) *Me {
	a, t := o.obj("account"), o.obj("token")
	return &Me{
		Account: Account{ID: a.str("id"), DisplayName: a.str("displayName"), CreatedAt: a.str("createdAt")},
		Token:   TokenInfo{ID: t.str("id"), Name: t.str("name"), Prefix: t.str("prefix"), CreatedAt: t.str("createdAt")},
	}
}

func parseLimits(o object) *Limits {
	return &Limits{PerMinute: parseWindow(o.obj("perMinute")), Daily: parseWindow(o.obj("daily"))}
}

func parseWindow(o object) QuotaWindow {
	return QuotaWindow{Limit: o.integer("limit"), Used: o.integer("used"), Remaining: o.integer("remaining"), ResetAt: o.integer("resetAt")}
}
