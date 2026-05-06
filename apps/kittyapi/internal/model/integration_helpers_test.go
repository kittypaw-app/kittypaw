//go:build integration

package model_test

import "strings"

func stripScheme(dsn string) string {
	dsn = strings.TrimPrefix(dsn, "postgres://")
	dsn = strings.TrimPrefix(dsn, "postgresql://")
	return dsn
}
