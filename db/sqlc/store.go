package db

import "github.com/jackc/pgx/v5/pgxpool"

// Store provides all functions to execute database queries. Today it is just
// the sqlc-generated Querier; transactional methods can be added here later.
type Store interface {
	Querier
}

// SQLStore implements Store using a pgx connection pool.
type SQLStore struct {
	connPool *pgxpool.Pool
	*Queries
}

// NewStore creates a Store backed by the given connection pool.
func NewStore(connPool *pgxpool.Pool) Store {
	return &SQLStore{
		connPool: connPool,
		Queries:  New(connPool),
	}
}
