package runtime

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mstoews/glutenfree-server/api"
	"github.com/mstoews/glutenfree-server/app"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/rs/zerolog/log"
)

// Start builds the connection pool and runs the HTTP server. It blocks until
// the server exits.
func Start(a *app.Application) error {
	cfg := a.Config

	poolCfg, err := pgxpool.ParseConfig(cfg.DBSource)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot parse db source")
	}
	poolCfg.MaxConns = 10
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.HealthCheckPeriod = time.Minute

	connPool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot connect to db")
	}

	// Warm the pool in the background so startup isn't gated on DB readiness.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := connPool.Ping(ctx); err != nil {
			log.Warn().Err(err).Msg("initial db ping failed; pool will retry on first request")
		}
	}()

	store := db.NewStore(connPool)

	server, err := api.NewServer(cfg, store)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot create server")
	}

	log.Info().Str("addr", cfg.HTTPServerAddress).Msg("starting http server")
	return server.Start(cfg.HTTPServerAddress)
}
