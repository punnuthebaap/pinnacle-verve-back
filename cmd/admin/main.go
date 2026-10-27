// Command admin bootstraps the first owner account. There are no default credentials: the password is
// read from ADMIN_PASSWORD (never a flag, so it stays out of shell history and process lists).
//
//	ADMIN_PASSWORD='…' go run ./cmd/admin create-owner -email owner@example.com -name "Owner Name"
//
// Further accounts are created by an owner in the admin panel.
//
// -dev-reset (APP_ENV=development only) upserts the given owner and resets its password, for local
// development and end-to-end tests. It is refused in production.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/platform/config"
	"github.com/pinnacleverve/api/internal/platform/database"
	"github.com/pinnacleverve/api/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "admin:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "create-owner" {
		return fmt.Errorf("usage: ADMIN_PASSWORD=… admin create-owner -email EMAIL -name NAME")
	}
	fs := flag.NewFlagSet("create-owner", flag.ContinueOnError)
	email := fs.String("email", "", "owner email")
	name := fs.String("name", "", "owner display name")
	devReset := fs.Bool("dev-reset", false, "development only: create or reset this owner")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	password := os.Getenv("ADMIN_PASSWORD")
	if !strings.Contains(*email, "@") || len(strings.TrimSpace(*name)) < 2 {
		return fmt.Errorf("-email and -name are required")
	}
	if len(password) < 12 {
		return fmt.Errorf("ADMIN_PASSWORD must be at least 12 characters")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
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
	q := store.New(pool)
	hash, err := admin.HashPassword(password)
	if err != nil {
		return err
	}
	if *devReset {
		if cfg.Env != "development" {
			return fmt.Errorf("-dev-reset is only allowed with APP_ENV=development")
		}
		if u, err := q.GetAdminByEmail(ctx, *email); err == nil {
			if err := q.SetAdminPassword(ctx, store.SetAdminPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
				return err
			}
			if _, err := q.UpdateAdmin(ctx, store.UpdateAdminParams{ID: u.ID, Name: u.Name, Role: "owner", IsActive: true}); err != nil {
				return err
			}
			fmt.Printf("owner %s reset (development)\n", u.Email)
			return nil
		}
	} else if n, err := q.CountActiveOwners(ctx); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("an active owner already exists; create further accounts in the admin panel")
	}
	u, err := q.InsertAdmin(ctx, store.InsertAdminParams{Email: *email, Name: strings.TrimSpace(*name), PasswordHash: hash, Role: "owner"})
	if err != nil {
		return err
	}
	fmt.Printf("owner %s created (%s)\n", u.Email, u.ID)
	return nil
}
