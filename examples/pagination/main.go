// Command pagination walks every page of a search with SearchAll. Each page
// costs one request of the daily quota, so -max bounds the walk.
//
//	POLYZON_API_KEY=pzk_... go run ./examples/pagination -q "кашпо" -max 120
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/PolyzonOrg/polyzon-go"
)

func main() {
	query := flag.String("q", "кашпо", "search query")
	limit := flag.Int("max", 120, "stop after this many models")
	flag.Parse()

	// Ctrl+C cancels the context, which aborts the request in flight.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := polyzon.NewClient()
	if err != nil {
		log.Fatal(err)
	}

	params := polyzon.SearchParams{Query: *query, Size: 50, Sort: polyzon.SortNewest}
	n := 0
	for item, err := range client.SearchAll(ctx, params) {
		if err != nil {
			log.Fatal(err)
		}
		n++
		fmt.Printf("%4d  %-14s %s\n", n, item.Source, item.Title)
		if n >= *limit {
			break // no further pages are fetched
		}
	}
}
