package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/argon/backend/internal/auth"
)

// Deps is what the API needs. Identity comes only from a session token
// (Authorization: Bearer), resolved against the database by session_valid.
type Deps struct {
	DB *pgxpool.Pool
}

type server struct{ db *pgxpool.Pool }

type callerKey struct{}

// NewServer returns the HTTP handler for every path in api/openapi.yaml,
// through the generated router and strict-server types, so a handler that
// drifts from the spec does not compile.
func NewServer(d Deps) (http.Handler, error) {
	if d.DB == nil {
		return nil, errors.New("api: Deps.DB is required")
	}
	s := &server{db: d.DB}
	h := HandlerFromMux(NewStrictHandler(s, nil), nil)
	return s.authenticate(h), nil
}

// Paths that need no session.
var public = map[string]bool{"/healthz": true}

// authenticate resolves the bearer token to a user before any handler runs.
// No token, an invented one, or a session that is ended, expired or whose
// holder lost the privilege it was granted under: 401.
func (s *server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if public[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		var user string
		if ok && tok != "" {
			if err := s.db.QueryRow(r.Context(),
				`SELECT user_id::text FROM auth_sessions WHERE token_digest = $1 AND session_valid($1)`,
				auth.TokenDigest(tok)).Scan(&user); err != nil {
				user = ""
			}
		}
		if user == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(Problem{Code: "unauthenticated", Message: "sign in again"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, user)))
	})
}

func callerOf(ctx context.Context) string {
	u, _ := ctx.Value(callerKey{}).(string)
	return u
}

func (s *server) GetHealthz(ctx context.Context, _ GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	if err := s.db.Ping(ctx); err != nil {
		return GetHealthz503Response{}, nil
	}
	return GetHealthz200Response{}, nil
}

// ErrNotBuilt: postAll has its own acceptance tests to come (LEDGER_CONTRACT
// §1). Until then the route answers with an error, never a pretend success.
var ErrNotBuilt = errors.New("api: POST /ledger/batches is not built yet")

func (s *server) PostLedgerBatches(context.Context, PostLedgerBatchesRequestObject) (PostLedgerBatchesResponseObject, error) {
	return nil, ErrNotBuilt
}
