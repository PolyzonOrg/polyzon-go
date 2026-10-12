// Command proxy calls the API through an explicit proxy (http, https or socks5,
// with optional user:password) and prints the account the token belongs to.
//
//	POLYZON_API_KEY=pzk_... go run ./examples/proxy -proxy http://user:pass@proxy.internal:3128
//
// Without -proxy the standard HTTPS_PROXY / NO_PROXY environment applies.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"

	"github.com/PolyzonOrg/polyzon-go"
)

func main() {
	proxy := flag.String("proxy", "", "proxy URL, e.g. socks5://127.0.0.1:1080")
	flag.Parse()

	var opts []polyzon.Option
	if *proxy != "" {
		proxyURL, err := url.Parse(*proxy)
		if err != nil {
			log.Fatal("invalid -proxy URL") // the URL may hold a password: do not echo it
		}
		opts = append(opts, polyzon.WithProxy(proxyURL))
	}
	client, err := polyzon.NewClient(opts...)
	if err != nil {
		log.Fatal(err)
	}

	me, err := client.Me(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("account %s (%s), token %q (%s…)\n", me.Account.ID, me.Account.DisplayName, me.Token.Name, me.Token.Prefix)
}
