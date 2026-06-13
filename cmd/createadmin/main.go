// Command createadmin bootstraps an internal ops admin account (for the
// /internal/* review queue). There is intentionally no public signup for
// internal admins.
//
//	go run ./cmd/createadmin -email ops@example.com -password 'secret123'
//	# or against an explicit DB (skips app.env):
//	go run ./cmd/createadmin -email ops@example.com -password 'secret123' -dbsource "$DB_URL"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/util"
)

func main() {
	email := flag.String("email", "", "internal admin email")
	password := flag.String("password", "", "internal admin password")
	dbsource := flag.String("dbsource", "", "override DB_SOURCE (otherwise read from app.env)")
	flag.Parse()

	if *email == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "createadmin: -email and -password are required")
		os.Exit(2)
	}

	if err := run(*email, *password, *dbsource); err != nil {
		fmt.Fprintln(os.Stderr, "createadmin:", err)
		os.Exit(1)
	}
}

func run(email, password, dbsource string) error {
	source := dbsource
	if source == "" {
		config, err := util.LoadConfig(".")
		if err != nil {
			return fmt.Errorf("load config (run from repo root or pass -dbsource): %w", err)
		}
		source = config.DBSource
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, source)
	if err != nil {
		return err
	}
	defer pool.Close()

	hash, err := util.HashPassword(password)
	if err != nil {
		return err
	}

	admin, err := db.NewStore(pool).CreateInternalAdmin(ctx, db.CreateInternalAdminParams{
		Email:        email,
		PasswordHash: hash,
	})
	if err != nil {
		return err
	}

	fmt.Printf("created internal admin %s (id %s)\n", admin.Email, admin.ID)
	return nil
}
