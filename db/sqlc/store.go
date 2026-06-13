package db

import "github.com/jackc/pgx/v5/pgxpool"

// Repository provides all functions to execute database queries. Today it is
// just the sqlc-generated Querier; transactional methods can be added here
// later. (Named Repository, not Store, to avoid clashing with the generated
// `Store` model for the stores table.)
type Repository interface {
	Querier
}

// SQLStore implements Repository using a pgx connection pool.
type SQLStore struct {
	connPool *pgxpool.Pool
	*Queries
}

// NewStore creates a Repository backed by the given connection pool.
func NewStore(connPool *pgxpool.Pool) Repository {
	return &SQLStore{
		connPool: connPool,
		Queries:  New(connPool),
	}
}
