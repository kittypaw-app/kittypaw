package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/kittypaw-app/kittyhome/internal/smoke"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := smoke.RunLocal(ctx, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "smoke failed: %v\n", err)
		os.Exit(1)
	}
}
