//go:build tier1

// Acceptance tests for GET /me — written before the handler, for review.
// Every TestApiMe* name is here.
//
// Finding 40: sign-in returned a session token and nothing else, so a client
// could learn no user, member, society or post, and every other endpoint
// needs one of those ids. Each endpoint was verified in isolation; the gap
// was in what joins them. /me is the join.
//
// Proposed (for review):
//   - The CALLER'S OWN records only: their user, each of their member records
//     (one per society), and the posts each holds TODAY. Nothing about anyone
//     else.
//   - A post is current when held_from <= today and held_until is unset or
//     today or later: an ended appointment, or one not yet begun, is not a
//     post. The member record stays listed either way.
//   - Empty lists are [] (never null): a user with no member record, a member
//     with no post.
//   - Memberships ordered by society code, so the answer is stable.
//   - No session, or an ended one: 401.
package tier1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/argon/backend/internal/auth"
)

type meWire struct {
	UserId      string `json:"userId"`
	FullName    string `json:"fullName"`
	Memberships []struct {
		SocietyId    string `json:"societyId"`
		SocietyCode  string `json:"societyCode"`
		SocietyName  string `json:"societyName"`
		MemberId     string `json:"memberId"`
		MemberNumber string `json:"memberNumber"`
		MemberStatus string `json:"memberStatus"`
		Posts        []struct {
			Post      string `json:"post"`
			Signatory bool   `json:"signatory"`
			HeldFrom  string `json:"heldFrom"`
		} `json:"posts"`
	} `json:"memberships"`
}

func getMe(t *testing.T, srv *httptest.Server, token string) resp {
	t.Helper()
	return get(t, srv, "/me", token)
}

func me(t *testing.T, srv *httptest.Server, user string) (meWire, string) {
	t.Helper()
	r := getMe(t, srv, signIn(t, user))
	if r.status != http.StatusOK {
		t.Fatalf("REFUSED — GET /me: %d %s", r.status, r.body)
	}
	var m meWire
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("WRONG SHAPE — %v: %s", err, r.body)
	}
	return m, string(r.body)
}

// memberFacts: what the society holds for a member record.
func memberFacts(t *testing.T, member string) (number, status string) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), `SELECT member_number, status::text FROM members WHERE id = $1`, member).Scan(&number, &status), "the member record")
	return
}

func societyCode(t *testing.T, society string) (code, name string) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), `SELECT code, registered_name FROM societies WHERE id = $1`, society).Scan(&code, &name), "the society")
	return
}

func TestApiMe01_AMemberSeesTheirOwnRecord(t *testing.T) {
	srv, s := apiServer(t), newSociety(t)
	u := s.user("Ngozi Member")
	m := s.member(u)
	got, raw := me(t, srv, u)
	num, status := memberFacts(t, m)
	code, name := societyCode(t, s.id)
	if got.UserId != u || got.FullName != "Ngozi Member" || len(got.Memberships) != 1 {
		t.Fatalf("WRONG CALLER — %s", raw)
	}
	ms := got.Memberships[0]
	if ms.SocietyId != s.id || ms.SocietyCode != code || ms.SocietyName != name || ms.MemberId != m || ms.MemberNumber != num || ms.MemberStatus != status {
		t.Fatalf("WRONG MEMBERSHIP — %+v; want society %s (%s, %s), member %s (%s, %s)", ms, s.id, code, name, m, num, status)
	}
	if ms.Posts == nil || len(ms.Posts) != 0 || !strings.Contains(raw, `"posts":[]`) {
		t.Fatalf("WRONG POSTS — a member with no post: %s; want []", raw)
	}
}

func TestApiMe02_AnOfficerSeesTheirCurrentPost(t *testing.T) {
	srv, s := apiServer(t), newSociety(t)
	u := s.user("Treasurer")
	s.signatory(u, "treasurer")
	got, raw := me(t, srv, u)
	if len(got.Memberships) != 1 || len(got.Memberships[0].Posts) != 1 {
		t.Fatalf("UNLISTED — the treasurer's post: %s", raw)
	}
	p := got.Memberships[0].Posts[0]
	if p.Post != "treasurer" || !p.Signatory || p.HeldFrom != "2020-01-01" {
		t.Fatalf("WRONG POST — %+v; want treasurer, a signatory, held from 2020-01-01", p)
	}
}

// An ended appointment is history, not a post: the record stays, the post goes.
func TestApiMe03_AnEndedPostIsNotListed(t *testing.T) {
	srv, s := apiServer(t), newSociety(t)
	u := s.user("Former auditor")
	m := s.member(u)
	fixture(t, s.appoint(m, "internal_auditor", false, "2020-01-01", "2021-01-01"), "an appointment that ended in 2021")
	got, raw := me(t, srv, u)
	if len(got.Memberships) != 1 || len(got.Memberships[0].Posts) != 0 {
		t.Fatalf("ACCEPTED — an appointment that ended in 2021 is listed as a post: %s", raw)
	}
}

func TestApiMe04_APostNotYetBegunIsNotListed(t *testing.T) {
	srv, s := apiServer(t), newSociety(t)
	u := s.user("Incoming secretary")
	m := s.member(u)
	fixture(t, s.appoint(m, "secretary", true, "2099-01-01", ""), "an appointment from 2099")
	got, raw := me(t, srv, u)
	if len(got.Memberships) != 1 || len(got.Memberships[0].Posts) != 0 {
		t.Fatalf("ACCEPTED — an appointment that begins in 2099 is listed as a post: %s", raw)
	}
}

// One person, a member of two societies: both, in society-code order.
func TestApiMe05_APersonInTwoSocietiesSeesBoth(t *testing.T) {
	srv, a, b := apiServer(t), newSociety(t), newSociety(t)
	u := a.user("Member of two")
	ma := a.member(u)
	mb := b.member(u)
	got, raw := me(t, srv, u)
	if len(got.Memberships) != 2 {
		t.Fatalf("UNLISTED — a member of two societies: %s", raw)
	}
	codeA, _ := societyCode(t, a.id)
	codeB, _ := societyCode(t, b.id)
	first, second := ma, mb
	if codeB < codeA {
		first, second = mb, ma
	}
	if got.Memberships[0].MemberId != first || got.Memberships[1].MemberId != second {
		t.Fatalf("UNORDERED — memberships %s then %s; want %s then %s (society code order)",
			got.Memberships[0].MemberId, got.Memberships[1].MemberId, first, second)
	}
}

// The caller's records only: nobody else in the society appears.
func TestApiMe06_OnlyTheCallersRecords(t *testing.T) {
	srv, s := apiServer(t), newSociety(t)
	u := s.user("Member")
	s.member(u)
	other := s.member(s.user("Another member"))
	officer := s.user("Chairman")
	s.signatory(officer, "chairman")
	got, raw := me(t, srv, u)
	if len(got.Memberships) != 1 || strings.Contains(raw, other) {
		t.Fatalf("LEAKED — the caller's /me carries another member's record: %s", raw)
	}
	gotO, rawO := me(t, srv, officer)
	if len(gotO.Memberships) != 1 || strings.Contains(rawO, other) {
		t.Fatalf("LEAKED — an officer's /me carries a member of their society: %s", rawO)
	}
}

func TestApiMe07_NoMemberRecordIsAnEmptyList(t *testing.T) {
	srv, s := apiServer(t), newSociety(t)
	u := s.user("Argon staff, no member record")
	got, raw := me(t, srv, u)
	if got.UserId != u || got.Memberships == nil || len(got.Memberships) != 0 || !strings.Contains(raw, `"memberships":[]`) {
		t.Fatalf("WRONG — a user with no member record: %s; want memberships []", raw)
	}
}

func TestApiMe08_NoSessionIsRefused(t *testing.T) {
	srv := apiServer(t)
	if r := getMe(t, srv, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("ACCEPTED — GET /me with no session: %d %s", r.status, r.body)
	}
}

func TestApiMe09_AnEndedSessionIsRefused(t *testing.T) {
	srv, s := apiServer(t), newSociety(t)
	u := s.user("Signed out")
	s.member(u)
	tok := signIn(t, u)
	fixture(t, one(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id WHERE token_digest = $1`, auth.TokenDigest(tok)), "end session")
	if r := getMe(t, srv, tok); r.status != http.StatusUnauthorized {
		t.Fatalf("ACCEPTED — GET /me on an ended session: %d %s", r.status, r.body)
	}
}
