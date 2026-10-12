// Command quickstart searches the Polyzon catalogue, opens the first model card
// and prints the remaining daily quota.
//
//	POLYZON_API_KEY=pzk_... go run ./examples/quickstart дракон
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/PolyzonOrg/polyzon-go"
)

func main() {
	query := "дракон"
	if len(os.Args) > 1 {
		query = strings.Join(os.Args[1:], " ")
	}
	ctx := context.Background()

	client, err := polyzon.NewClient() // reads POLYZON_API_KEY
	if err != nil {
		log.Fatal(err)
	}

	page, err := client.Search(ctx, polyzon.SearchParams{Query: query, Size: 5})
	if errors.Is(err, polyzon.ErrAuthentication) {
		log.Fatal("the token is invalid or revoked: issue a new one at https://polyzon.org/account/")
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d models for %q\n", page.Total, page.Query)
	for _, item := range page.Items {
		fmt.Printf("  %-12s %s\n", item.Source, item.Title)
	}
	if len(page.Items) == 0 {
		return
	}

	first := page.Items[0]
	model, err := client.GetModel(ctx, first.Source, first.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%s\n%s\n%s\n", model.Title, model.URL, model.Description)

	if rl := model.Meta.RateLimit; rl != nil {
		fmt.Printf("\n%d of %d daily requests left\n", rl.Remaining, rl.Limit)
	}
}
