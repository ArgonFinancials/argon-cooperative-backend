package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps is what the API needs. Identity comes only from a session token
// (Authorization: Bearer), resolved against the database by session_valid.
type Deps struct {
	DB *pgxpool.Pool
}

// ErrNotImplemented: the handlers are written after their acceptance tests
// (test/tier1, TestApi*) have been reviewed.
var ErrNotImplemented = errors.New("api: not implemented — tests first")

// NewServer returns the HTTP handler for every path in api/openapi.yaml.
func NewServer(d Deps) (http.Handler, error) { return nil, ErrNotImplemented }
