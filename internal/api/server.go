package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/argon/backend/internal/auth"
)

// Deps is what the API needs. Identity comes only from a session token
// (Authorization: Bearer), resolved against the database by session_valid.
type Deps struct {
	DB   *pgxpool.Pool
	Log  *slog.Logger // optional; slog.Default() when nil
	Auth auth.Service // the step-up (POST /auth/pin-proofs); optional until it is built
}

type server struct {
	db   *pgxpool.Pool
	log  *slog.Logger
	auth auth.Service
}

type (
	callerKey struct{}
	tokenKey  struct{} // the bearer token itself: the step-up re-proves the PIN on THIS session
)

// NewServer returns the HTTP handler for every path in api/openapi.yaml,
// through the generated router and strict-server types, so a handler that
// drifts from the spec does not compile.
func NewServer(d Deps) (http.Handler, error) {
	if d.DB == nil {
		return nil, errors.New("api: Deps.DB is required")
	}
	s := &server{db: d.DB, log: d.Log, auth: d.Auth}
	if s.log == nil {
		s.log = slog.Default()
	}
	h := HandlerFromMux(NewStrictHandlerWithOptions(s, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  RequestError,
		ResponseErrorHandlerFunc: ResponseError,
	}), nil)
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
			writeProblem(w, http.StatusUnauthorized, Problem{Code: "unauthenticated", Message: "sign in again"})
			return
		}
		ctx := context.WithValue(context.WithValue(r.Context(), callerKey{}, user), tokenKey{}, tok)
		next.ServeHTTP(w, r.WithContext(ctx))
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

// PostLedgerBatch (postAll): batch.go.

// ErrStepUpNotBuilt: POST /auth/pin-proofs has its acceptance tests written and
// under review (test/tier1/tier1_api_stepup_test.go). Until the handler is
// written against them it mints nothing.
var ErrStepUpNotBuilt = errors.New("api: POST /auth/pin-proofs is not built yet")

func (s *server) PostPinProof(context.Context, PostPinProofRequestObject) (PostPinProofResponseObject, error) {
	return nil, ErrStepUpNotBuilt
}

// RequestError answers a request the generated layer could not decode — money
// sent as a JSON number, a body that is not JSON — as a JSON Problem, never
// text/plain. Exported so the test fakes answer the same way.
func RequestError(w http.ResponseWriter, _ *http.Request, err error) {
	writeProblem(w, http.StatusBadRequest, Problem{Code: "bad_request", Message: err.Error()})
}

// ResponseError: a handler failed. The cause stays in the server's log; the
// caller is told only that it failed. (A handler that writes says for itself
// what was written: postAll answers its own 500.)
func ResponseError(w http.ResponseWriter, _ *http.Request, err error) {
	writeProblem(w, http.StatusInternalServerError, Problem{Code: "internal", Message: "the request failed"})
}

func writeProblem(w http.ResponseWriter, status int, p Problem) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(p)
}
