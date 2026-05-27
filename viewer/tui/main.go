package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/endigma/sap/state"
	"github.com/endigma/sap/transport/sse"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/live", "SSE endpoint URL")
	allowUnknownParents := flag.Bool("allow-unknown-parents", false, "show spans with non-empty parent IDs that are not currently known as roots")
	flag.Parse()

	ctx := context.Background()
	records, states := sse.Stream(ctx, *url)
	storeOptions := state.StoreOptions{AllowUnknownParents: *allowUnknownParents}
	if err := run(ctx, records, states, storeOptions); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
