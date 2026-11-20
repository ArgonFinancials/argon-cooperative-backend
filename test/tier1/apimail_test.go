//go:build tier1

// The test email sender (0034): every API server in these tests is given it.
package tier1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
	"github.com/argon/backend/internal/email"
)

// testMail: the email sender every API server here is given (0034): it keeps
// each message, so a test can read the code an officer never sees, and can
// be told to fail.
var testMail = &mailbox{}

type mailbox struct {
	mu   sync.Mutex
	sent []email.Message
	fail bool
}

func (m *mailbox) Send(_ context.Context, msg email.Message) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return "", errors.New("test mailbox: refused, as a provider would")
	}
	m.sent = append(m.sent, msg)
	return fmt.Sprintf("test-msg-%d", len(m.sent)), nil
}

// to: the messages sent to addr so far.
func (m *mailbox) to(addr string) []email.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []email.Message
	for _, s := range m.sent {
		if s.To == addr {
			out = append(out, s)
		}
	}
	return out
}

func (m *mailbox) failing(t *testing.T) {
	m.mu.Lock()
	m.fail = true
	m.mu.Unlock()
	t.Cleanup(func() { m.mu.Lock(); m.fail = false; m.mu.Unlock() })
}

// apiServerNoEmail: as apiServer, with no email sender configured.
func apiServerNoEmail(t *testing.T) *httptest.Server {
	t.Helper()
	var h http.Handler
	if brk, ok := os.LookupEnv("ARGON_TEST_API_FAKE"); ok {
		h = newFakeAPIWith(&fakeAPI{db: db, brk: brk, noEmail: true})
	} else {
		app, authp := rolePools(t, 0)
		svc, err := auth.New(auth.Deps{DB: authp, SMS: &fakeSMS{}, Notify: &fakeNotifier{}, CodeKey: []byte("test-code-key-not-a-secret"), Argon: testArgon})
		if err != nil {
			t.Fatalf("FIXTURE: cannot construct the auth service: %v", err)
		}
		if h, err = api.NewServer(api.Deps{DB: app, Auth: svc, CSRFKey: testCSRFKey}); err != nil {
			t.Fatalf("FIXTURE: cannot construct the API: %v", err)
		}
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}
