package main

import (
	"context"
	"fmt"
	"os"

	"github.com/kittypaw-app/kittyspace/internal/smoke"
)

func main() {
	cfg, err := smoke.LoadRemoteConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cutover smoke config: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	if err := smoke.RunRemote(ctx, cfg, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "cutover smoke failed: %v\n", err)
		os.Exit(1)
	}
}
