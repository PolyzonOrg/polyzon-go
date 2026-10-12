// Package polyzon is the official Go client for the Polyzon public API
// (https://api.polyzon.org): search across 3D-model catalogues and fetch model
// cards, with the account's quota and token information.
//
// Create one [Client] and share it: it is safe for concurrent use by many
// goroutines and keeps a pool of keep-alive connections.
//
//	client, err := polyzon.NewClient() // reads POLYZON_API_KEY
//	if err != nil {
//		log.Fatal(err)
//	}
//	page, err := client.Search(ctx, polyzon.SearchParams{Query: "дракон"})
//
// Every method takes a [context.Context] first; cancelling it aborts the request
// and any retry wait. Errors returned by the API are [*APIError] values that match
// the sentinel errors of this package through [errors.Is], for example
// errors.Is(err, polyzon.ErrRateLimited).
//
// Keep the token on the server side: never ship it to browsers or mobile apps.
package polyzon

// Version is the version of this SDK. It is sent in the User-Agent header.
const Version = "1.0.0"

// Known sources (catalogues) of models. The API may add new sources at any time,
// so every field and parameter holding a source is a plain string.
const (
	SourceThingiverse   = "thingiverse"
	SourceCults3D       = "cults3d"
	SourcePrintables    = "printables"
	SourceMakerWorld    = "makerworld"
	Source3DToday       = "3dtoday"
	SourceCrealityCloud = "crealitycloud"
	SourceMyMiniFactory = "myminifactory"
	SourceThangs        = "thangs"
	SourceYouMagine     = "youmagine"
	SourceGrabCAD       = "grabcad"
	SourceSketchfab     = "sketchfab"
	SourceThreeding     = "threeding"
	SourceNexprint      = "nexprint"
	SourceNASA          = "nasa"
	SourceNIH3D         = "nih3d"
	SourceSmithsonian   = "smithsonian"
	SourcePinshape      = "pinshape"
	SourceCGTrader      = "cgtrader"
	SourceGambody       = "gambody"
	SourceJewelModel    = "jewelmodel"
	SourceMakerOnline   = "makeronline"
	Source3DExport      = "3dexport"
)

// Known license ids, used by [SearchParams.Licenses], [SearchItem.License] and
// [License.ID]. New ids may appear, so the fields are plain strings.
const (
	LicenseCC0        = "cc0"
	LicenseCCBY       = "cc-by"
	LicenseCCBYSA     = "cc-by-sa"
	LicenseCCBYND     = "cc-by-nd"
	LicenseCCBYNC     = "cc-by-nc"
	LicenseCCBYNCSA   = "cc-by-nc-sa"
	LicenseCCBYNCND   = "cc-by-nc-nd"
	LicenseGPL        = "gpl"
	LicenseLGPL       = "lgpl"
	LicenseMIT        = "mit"
	LicenseBSD        = "bsd"
	LicensePrivateUse = "private-use"
	LicenseCommercial = "commercial"
	LicenseCustom     = "custom"
	LicenseUnknown    = "unknown"
)

// Sort orders for [SearchParams.Sort].
const (
	SortRelevant  = "relevant" // by relevance (the default)
	SortPopular   = "popular"  // by popularity on the source
	SortNewest    = "newest"
	SortLikes     = "likes"
	SortMakes     = "makes"
	SortDownloads = "downloads"
)

// Languages for titles, descriptions and tags ([SearchParams.Lang], [WithLang]).
// Texts originally in another language are machine-translated.
const (
	LangRU = "ru" // the default
	LangEN = "en"
)

// Values of [Price.Type].
const (
	PriceFree = "free"
	PricePaid = "paid"
	PriceOpen = "open"
)

// Values of [SearchPage.TotalRelation].
const (
	TotalEq  = "eq"  // Total is exact
	TotalGte = "gte" // Total is a lower bound
)

// Error codes of [APIError.Code].
const (
	CodeBadRequest            = "bad_request"             // 400, see APIError.Param
	CodePageTooDeep           = "page_too_deep"           // 400, page × size > 2400
	CodeUnauthorized          = "unauthorized"            // 401, no token sent
	CodeInvalidToken          = "invalid_token"           // 401, unknown or revoked token
	CodeAccountActionRequired = "account_action_required" // 403, accept the terms at polyzon.org/account
	CodeNotFound              = "not_found"               // 404
	CodeGone                  = "gone"                    // 410, removed on the right holder's request
	CodeRateLimited           = "rate_limited"            // 429, per-minute limit
	CodeQuotaExceeded         = "quota_exceeded"          // 429, daily quota, resets at 00:00 UTC
	CodeUnavailable           = "unavailable"             // 503
	CodeInternal              = "internal"                // 500
)
