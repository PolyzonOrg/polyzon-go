package polyzon

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// maxPage is the deepest page the API serves (page > 100 answers 400), even
// while hasMore is still true there (size < 24 with 2400+ results).
const maxPage = 100

// SearchParams are the parameters of [Client.Search]. Zero values are not sent,
// so the server defaults apply.
type SearchParams struct {
	// Query is the search text in Russian or English, up to 200 characters. Required.
	Query string
	// Page is the page number, 1–100 (default 1).
	Page int
	// Size is the number of models per page, 1–50 (default 20). Page × Size must
	// not exceed 2400, else the API answers 400 page_too_deep.
	Size int
	// Sort is one of the Sort* constants (default SortRelevant).
	Sort string
	// Free restricts results to free models.
	Free bool
	// Sources restricts results to these sources (the Source* constants).
	Sources []string
	// Formats restricts results to these file formats, e.g. "stl", "3mf".
	Formats []string
	// Licenses restricts results to these license ids (the License* constants).
	Licenses []string
	// Lang is the language of titles and tags, LangRU (default) or LangEN.
	Lang string
}

func (p SearchParams) values() url.Values {
	v := url.Values{}
	set := func(name, value string) {
		if value != "" {
			v.Set(name, value)
		}
	}
	set("q", p.Query)
	if p.Page != 0 {
		v.Set("page", strconv.Itoa(p.Page))
	}
	if p.Size != 0 {
		v.Set("size", strconv.Itoa(p.Size))
	}
	set("sort", p.Sort)
	if p.Free {
		v.Set("free", "1")
	}
	set("sources", strings.Join(p.Sources, ","))
	set("formats", strings.Join(p.Formats, ","))
	set("license", strings.Join(p.Licenses, ","))
	set("lang", p.Lang)
	return v
}

// Search returns one page of models matching params. It costs one request of
// the daily quota (even when the API rejects the parameters).
//
// Like every method, it retries connection errors, timeouts, HTTP 500/502/503/504
// and 429 rate_limited up to the configured number of times (see
// [WithMaxRetries]), honouring Retry-After up to 60 seconds. A daily quota error
// (429 quota_exceeded) is returned at once.
func (c *Client) Search(ctx context.Context, params SearchParams) (*SearchPage, error) {
	o, meta, err := c.get(ctx, "/v1/search", params.values())
	if err != nil {
		return nil, err
	}
	page := parseSearchPage(o)
	page.Meta = meta
	return page, nil
}

// SearchAll iterates over all results of params, fetching pages lazily from
// params.Page (default 1) until a page reports no more results, comes back
// empty, or is page 100, the deepest the API serves. Every page costs one request of
// the daily quota; break out of the loop to stop fetching. On an error the
// iterator yields it once and stops.
//
//	for item, err := range client.SearchAll(ctx, polyzon.SearchParams{Query: "дракон", Size: 50}) {
//		if err != nil {
//			return err
//		}
//		fmt.Println(item.Title)
//	}
//
// The API serves at most 2400 results per query (page × size).
func (c *Client) SearchAll(ctx context.Context, params SearchParams) iter.Seq2[SearchItem, error] {
	return func(yield func(SearchItem, error) bool) {
		p := params // a fresh copy per range loop, so the iterator can be reused
		if p.Page == 0 {
			p.Page = 1
		}
		for {
			page, err := c.Search(ctx, p)
			if err != nil {
				yield(SearchItem{}, err)
				return
			}
			for _, item := range page.Items {
				if !yield(item, nil) {
					return
				}
			}
			if !page.HasMore || len(page.Items) == 0 || p.Page >= maxPage {
				return
			}
			p.Page++
		}
	}
}

// ModelOption tunes [Client.GetModel] and [Client.GetModels].
type ModelOption func(*modelOptions)

type modelOptions struct {
	lang        string
	concurrency int
}

// WithLang sets the language of the title, description and tags: [LangRU]
// (default) or [LangEN].
func WithLang(lang string) ModelOption {
	return func(o *modelOptions) { o.lang = lang }
}

// WithConcurrency sets how many requests [Client.GetModels] runs at once
// (default 4, at least 1). [Client.GetModel] ignores it.
func WithConcurrency(n int) ModelOption {
	return func(o *modelOptions) { o.concurrency = n }
}

// Server-side shape of a model key; checked before any request so that a bad
// value never reaches the URL (an id of "." or ".." would be normalised away).
var (
	sourcePattern = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,255}$`)
)

func modelPath(source, id string) (string, error) {
	if !sourcePattern.MatchString(source) {
		return "", fmt.Errorf("polyzon: invalid model source %q: want 1-32 characters of a-z and 0-9", source)
	}
	if !idPattern.MatchString(id) || id == "." || id == ".." {
		return "", fmt.Errorf("polyzon: invalid model id %q: want 1-255 characters of A-Z, a-z, 0-9, '.', '_' and '-'", id)
	}
	return "/v1/models/" + url.PathEscape(source) + "/" + url.PathEscape(id), nil
}

// GetModel returns the model card of the model id on source, the same pair as in
// search results and in polyzon.org/model/{source}/{id}. It costs one request of
// the daily quota. An invalid source or id is reported without a request.
//
// A removed model answers [ErrGone]: delete your copy of it too.
func (c *Client) GetModel(ctx context.Context, source, id string, opts ...ModelOption) (*Model, error) {
	var o modelOptions
	for _, opt := range opts {
		opt(&o)
	}
	path, err := modelPath(source, id)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	if o.lang != "" {
		query.Set("lang", o.lang)
	}
	body, meta, err := c.get(ctx, path, query)
	if err != nil {
		return nil, err
	}
	model := parseModel(body)
	model.Meta = meta
	return model, nil
}

// ModelResult is the outcome of one reference in [Client.GetModels]: exactly
// one of Model and Err is set.
type ModelResult struct {
	Ref   ModelRef
	Model *Model
	Err   error
}

// GetModels fetches many model cards with bounded parallelism ([WithConcurrency],
// default 4) and returns one result per ref in input order. Failures are reported
// per result, never as the returned error, which is only for invalid options.
//
// Once a request fails with [ErrAuthentication], [ErrPermissionDenied] or
// [ErrQuotaExceeded], the refs not started yet get that same error without
// requests. Each fetched model costs one request of the daily quota.
func (c *Client) GetModels(ctx context.Context, refs []ModelRef, opts ...ModelOption) ([]ModelResult, error) {
	o := modelOptions{concurrency: 4}
	for _, opt := range opts {
		opt(&o)
	}
	if o.concurrency < 1 {
		return nil, errors.New("polyzon: concurrency must be at least 1")
	}
	results := make([]ModelResult, len(refs))
	slots := make(chan struct{}, o.concurrency)
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		fatal error
	)
	for i, ref := range refs {
		results[i].Ref = ref
		slots <- struct{}{}
		mu.Lock()
		stop := fatal
		mu.Unlock()
		if stop != nil {
			results[i].Err = stop
			<-slots
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			model, err := c.GetModel(ctx, ref.Source, ref.ID, opts...)
			results[i].Model, results[i].Err = model, err
			if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrPermissionDenied) || errors.Is(err, ErrQuotaExceeded) {
				mu.Lock()
				if fatal == nil {
					fatal = err
				}
				mu.Unlock()
			}
			<-slots
		}()
	}
	wg.Wait()
	return results, nil
}

// Me returns the account and the token used for the request. It does not use
// the daily quota.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	body, meta, err := c.get(ctx, "/v1/me", nil)
	if err != nil {
		return nil, err
	}
	me := parseMe(body)
	me.Meta = meta
	return me, nil
}

// Limits returns the account's per-minute and daily quota windows. It does not
// use the daily quota (it counts toward the per-minute limit like every method).
func (c *Client) Limits(ctx context.Context) (*Limits, error) {
	body, meta, err := c.get(ctx, "/v1/limits", nil)
	if err != nil {
		return nil, err
	}
	limits := parseLimits(body)
	limits.Meta = meta
	return limits, nil
}
