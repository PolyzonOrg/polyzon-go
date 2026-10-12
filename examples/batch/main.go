// Command batch fetches the model cards of a search page concurrently with
// GetModels and reports each result in input order.
//
//	POLYZON_API_KEY=pzk_... go run ./examples/batch -q "robot" -concurrency 4
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/PolyzonOrg/polyzon-go"
)

func main() {
	query := flag.String("q", "robot", "search query")
	size := flag.Int("n", 8, "number of models to fetch")
	concurrency := flag.Int("concurrency", 4, "requests in flight")
	flag.Parse()
	ctx := context.Background()

	client, err := polyzon.NewClient()
	if err != nil {
		log.Fatal(err)
	}

	page, err := client.Search(ctx, polyzon.SearchParams{Query: *query, Size: *size, Lang: polyzon.LangEN})
	if err != nil {
		log.Fatal(err)
	}
	refs := make([]polyzon.ModelRef, 0, len(page.Items))
	for _, item := range page.Items {
		refs = append(refs, polyzon.ModelRef{Source: item.Source, ID: item.ID})
	}

	results, err := client.GetModels(ctx, refs, polyzon.WithConcurrency(*concurrency), polyzon.WithLang(polyzon.LangEN))
	if err != nil {
		log.Fatal(err)
	}
	for _, res := range results {
		switch {
		case res.Err == nil:
			fmt.Printf("%s/%s  %s  (%d images)\n", res.Ref.Source, res.Ref.ID, res.Model.Title, len(res.Model.Images))
		case errors.Is(res.Err, polyzon.ErrGone):
			fmt.Printf("%s/%s  removed on the right holder's request\n", res.Ref.Source, res.Ref.ID)
		case errors.Is(res.Err, polyzon.ErrQuotaExceeded):
			log.Fatalf("daily quota spent: %v", res.Err)
		default:
			fmt.Printf("%s/%s  error: %v\n", res.Ref.Source, res.Ref.ID, res.Err)
		}
	}
}
