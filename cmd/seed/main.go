// Command seed applies migrations and loads development fixtures. Refuses to run in production.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/pinnacleverve/api/db"
	"github.com/pinnacleverve/api/internal/platform/config"
	"github.com/pinnacleverve/api/internal/platform/database"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
	fmt.Println("seed: development fixtures loaded")
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProduction() {
		return fmt.Errorf("refusing to seed fixtures in production")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, db.DevSeed) // no args → simple protocol, multi-statement allowed
	return err
}
