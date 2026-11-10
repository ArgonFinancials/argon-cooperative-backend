package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// THE REGISTER over HTTP (2026-11-10; the reviewer: members before the
// wallet; the spec agreed with the frontend before the code). Written against
// test/tier1/tier1_api_members_test.go and its reference, apifake_members_test.go.
//
//   FIND: officers of the society only (the auditor included), by number,
//   phone (any Nigerian form) or part of a name, with what the issuing screen
//   needs. BRANCHES: the same readers.
//   REGISTER: the society's secretary, on their own step-up (member_register),
//   through 0031's register_member, the one way a member is added. The phone
//   is put in +234 form here; every other rule is the database's too.
//   IMPORT: ONE transaction, all or nothing; every refused row reported at
//   once, before the step-up is spent; the client's reference recorded with
//   the members, never before; a repeat answers the first commit's map.

var e164NG = regexp.MustCompile(`^\+234[0-9]{10}$`)

// nigerianPhone: 0803…, 234803… and +234803…, spaces and dashes ignored, as
// +234 E.164. Anything else is returned as given, for the rules to refuse.
func nigerianPhone(p string) string {
	s := strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(p))
	switch {
	case strings.HasPrefix(s, "+234"):
		return s
	case strings.HasPrefix(s, "234") && len(s) == 13:
		return "+" + s
	case strings.HasPrefix(s, "0") && len(s) == 11:
		return "+234" + s[1:]
	}
	return s
}

func (s *server) holdsPostIn(ctx context.Context, user, society, post string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND ($3 = '' OR a.post::text = $3)
		  AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))`, user, society, post).Scan(&ok)
	return ok, err
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func memberSummary(ctx context.Context, q querier, member string) (MemberSummary, error) {
	var m MemberSummary
	var id, status, enrol string
	var joined time.Time
	err := q.QueryRow(ctx, `SELECT m.id::text, m.member_number, m.full_name, m.phone, b.code, m.status::text, m.joined_on,
		  EXISTS (SELECT 1 FROM auth_credentials c WHERE c.user_id = m.user_id),
		  EXISTS (SELECT 1 FROM auth_devices d WHERE d.user_id = m.user_id AND d.revoked_at IS NULL),
		  CASE WHEN EXISTS (SELECT 1 FROM auth_devices d WHERE d.user_id = m.user_id AND d.revoked_at IS NULL) THEN 'enrolled'
		       WHEN EXISTS (SELECT 1 FROM enrolment_codes c WHERE c.member_id = m.id AND c.used_at IS NULL AND c.voided_at IS NULL
		                     AND c.expires_at > now() AND NOT c.pending) THEN 'code_live'
		       WHEN EXISTS (SELECT 1 FROM enrolment_codes c WHERE c.member_id = m.id AND c.used_at IS NULL AND c.voided_at IS NULL
		                     AND c.expires_at > now() AND c.pending) THEN 'code_pending'
		       ELSE 'none' END,
		  EXISTS (SELECT 1 FROM ledger_entries e JOIN ledger_lines l ON l.entry_id = e.id JOIN accounts a ON a.id = l.account_id
		           WHERE e.kind = 'opening_balance' AND e.reverses_id IS NULL AND l.member_id = m.id AND a.role = 'member_savings'
		             AND NOT EXISTS (SELECT 1 FROM ledger_entries r WHERE r.reverses_id = e.id))
		FROM members m JOIN branches b ON b.id = m.branch_id WHERE m.id = $1`, member).
		Scan(&id, &m.MemberNumber, &m.FullName, &m.Phone, &m.BranchCode, &status, &joined, &m.HasPin, &m.HasTrustedDevice, &enrol, &m.OpeningBalancePosted)
	if err != nil {
		return m, err
	}
	m.MemberId, m.Status, m.Enrolment = uuidOf(id), MemberSummaryStatus(status), MemberSummaryEnrolment(enrol)
	m.JoinedOn = openapi_types.Date{Time: joined}
	rows, err := q.Query(ctx, `SELECT post::text, is_signatory FROM appointments WHERE member_id = $1
		AND held_from <= current_date AND (held_until IS NULL OR held_until >= current_date) ORDER BY post`, member)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	m.Posts = []struct {
		IsSignatory bool                   `json:"isSignatory"`
		Post        MemberSummaryPostsPost `json:"post"`
	}{}
	for rows.Next() {
		var post string
		var sig bool
		if err := rows.Scan(&post, &sig); err != nil {
			return m, err
		}
		m.Posts = append(m.Posts, struct {
			IsSignatory bool                   `json:"isSignatory"`
			Post        MemberSummaryPostsPost `json:"post"`
		}{sig, MemberSummaryPostsPost(post)})
	}
	return m, rows.Err()
}

var memberNotFound = Problem{Code: "not_found", Message: "no such society"}

func (s *server) FindMembers(ctx context.Context, req FindMembersRequestObject) (FindMembersResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.holdsPostIn(ctx, callerOf(ctx), society, ""); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return FindMembers404JSONResponse(memberNotFound), nil
	}
	q, limit, after := "", 50, ""
	if req.Params.Q != nil {
		q = strings.TrimSpace(*req.Params.Q)
	}
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	if req.Params.After != nil {
		after = *req.Params.After
	}
	rows, err := s.db.Query(ctx, `SELECT id::text FROM members WHERE society_id = $1
		  AND ($2 = '' OR lower(member_number) = lower($2) OR phone = $3 OR full_name ILIKE '%' || $2 || '%')
		  AND ($4 = '' OR member_number > $4)
		ORDER BY member_number LIMIT $5`, society, q, nigerianPhone(q), after, limit+1)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	page := MemberPage{Items: []MemberSummary{}}
	for i, id := range ids {
		if i == limit {
			n := page.Items[limit-1].MemberNumber
			page.Next = &n
			break
		}
		m, err := memberSummary(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, m)
	}
	return FindMembers200JSONResponse(page), nil
}

func (s *server) ListBranches(ctx context.Context, req ListBranchesRequestObject) (ListBranchesResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.holdsPostIn(ctx, callerOf(ctx), society, ""); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ListBranches404JSONResponse(memberNotFound), nil
	}
	rows, err := s.db.Query(ctx, `SELECT code, name FROM branches WHERE society_id = $1 ORDER BY code`, society)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := ListBranches200JSONResponse{Items: []struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}{}}
	for rows.Next() {
		var c, n string
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}{c, n})
	}
	return out, rows.Err()
}

type newRegistrant struct {
	line                                 int
	number, name, phone, branch, channel string
	joined                               time.Time
	nokName, nokPhone, nokRel            string
}

func registrantOf(line int, number, name, phone, branch, channel string, joined time.Time, nok *NextOfKin) newRegistrant {
	r := newRegistrant{line: line, number: strings.TrimSpace(number), name: name, phone: nigerianPhone(phone), branch: branch, channel: channel, joined: joined}
	if nok != nil {
		r.nokName, r.nokPhone, r.nokRel = nok.FullName, nigerianPhone(nok.Phone), nok.Relationship
	}
	return r
}

// refusalOf: why this member cannot be registered, or "". The database's
// rules (0031) are the same; asked here first so every refused row of an
// import is reported at once, before the step-up is spent.
func refusalOf(ctx context.Context, q querier, society string, r newRegistrant) (string, string, error) {
	var branch, taken bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM branches WHERE society_id = $1 AND code = $2),
		EXISTS (SELECT 1 FROM members WHERE society_id = $1 AND member_number = $3)`, society, r.branch, r.number).Scan(&branch, &taken); err != nil {
		return "", "", err
	}
	nok := 0
	for _, v := range []string{r.nokName, r.nokPhone, r.nokRel} {
		if strings.TrimSpace(v) != "" {
			nok++
		}
	}
	switch {
	case strings.TrimSpace(r.name) == "":
		return "name_missing", "a member is registered by name", nil
	case !e164NG.MatchString(r.phone):
		return "phone_not_nigerian", fmt.Sprintf("%q is not a Nigerian phone number", r.phone), nil
	case !branch:
		return "branch_unknown", fmt.Sprintf("this society has no branch %q", r.branch), nil
	case r.joined.After(time.Now()):
		return "joined_in_future", "joined on a day that has not come", nil
	case nok != 0 && (nok != 3 || !e164NG.MatchString(r.nokPhone)):
		return "next_of_kin_incomplete", "a next of kin is a name, a Nigerian phone and a relationship, or nothing", nil
	case taken:
		return "number_already_registered", fmt.Sprintf("%s is already in this society's register", r.number), nil
	}
	return "", "", nil
}

func registerOne(ctx context.Context, x pgx.Tx, society, act string, batch any, r newRegistrant) (string, error) {
	var line any
	if r.line > 0 {
		line = r.line
	}
	var id string
	err := x.QueryRow(ctx, `SELECT register_member($1, $2, $3, $4, $5, $6::date, $7::collection_channel, nullif($8, ''), nullif($9, ''), nullif($10, ''), $11, $12, $13)::text`,
		society, r.branch, r.number, r.name, r.phone, r.joined.Format("2006-01-02"), r.channel, r.nokName, r.nokPhone, r.nokRel, act, batch, line).Scan(&id)
	return id, err
}

type refusedLines = []struct {
	Code   string  `json:"code"`
	Detail *string `json:"detail,omitempty"`
	Line   int     `json:"line"`
}

func (s *server) RegisterMember(ctx context.Context, req RegisterMemberRequestObject) (RegisterMemberResponseObject, error) {
	caller, society := callerOf(ctx), req.SocietyId.String()
	if ok, err := s.holdsPostIn(ctx, caller, society, ""); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return RegisterMember404JSONResponse(memberNotFound), nil
	}
	if ok, err := s.holdsPostIn(ctx, caller, society, "secretary"); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return RegisterMember403JSONResponse{Code: "not_permitted", Message: "registering members is the secretary's"}, nil
	}
	b := req.Body
	if b == nil {
		return RegisterMember400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	r := registrantOf(0, b.MemberNumber, b.FullName, b.Phone, b.BranchCode, string(b.CollectionChannel), b.JoinedOn.Time, b.NextOfKin)
	code, msg, err := refusalOf(ctx, s.db, society, r)
	if err != nil {
		return nil, err
	}
	if code != "" {
		return RegisterMember422JSONResponse{Code: RegisterRefusalCode(code), Message: msg, Rows: refusedLines{}}, nil
	}
	var id string
	err = s.onStepUp(ctx, "member_register", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		var err error
		id, err = registerOne(ctx, x, society, act, nil, r)
		return err
	})
	if err != nil {
		if stepUpRefused[refusedBy(err)] {
			return RegisterMember403JSONResponse{Code: "step_up_required", Message: "register with a fresh PIN proof for member_register, your own"}, nil
		}
		return nil, err
	}
	m, err := memberSummary(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	return RegisterMember201JSONResponse(m), nil
}

var errRegisterRefused = errors.New("refused")

func (s *server) RegisterMembers(ctx context.Context, req RegisterMembersRequestObject) (RegisterMembersResponseObject, error) {
	caller, society, ref := callerOf(ctx), req.SocietyId.String(), req.Params.IdempotencyKey
	if ok, err := s.holdsPostIn(ctx, caller, society, ""); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return RegisterMembers404JSONResponse(memberNotFound), nil
	}
	if ok, err := s.holdsPostIn(ctx, caller, society, "secretary"); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return RegisterMembers403JSONResponse{Code: "not_permitted", Message: "registering members is the secretary's"}, nil
	}
	b := req.Body
	if b == nil {
		return RegisterMembers400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var out RegisterMembersResponseObject
	err := pgx.BeginFunc(ctx, s.db, func(x pgx.Tx) error {
		// One import at a time per society: two files naming one number cannot both register it.
		if _, err := x.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('argon.register:' || $1))`, society); err != nil {
			return err
		}
		if rec, ok, err := registeredRecord(ctx, x, society, ref); err != nil || ok {
			if ok {
				out = RegisterMembers200JSONResponse(rec)
			}
			return err
		}
		if len(b.Rows) == 0 {
			out = RegisterMembers422JSONResponse{Code: "nothing_to_register", Message: "No row in this file to register.", Rows: refusedLines{}}
			return errRegisterRefused
		}
		rows := make([]newRegistrant, 0, len(b.Rows))
		bad := refusedLines{}
		seen := map[string]bool{}
		for _, row := range b.Rows {
			r := registrantOf(row.Line, row.MemberNumber, row.FullName, row.Phone, row.BranchCode, string(row.CollectionChannel), row.JoinedOn.Time, row.NextOfKin)
			code, msg, err := refusalOf(ctx, x, society, r)
			if err != nil {
				return err
			}
			key := strings.ToLower(r.number)
			if code == "" && seen[key] {
				code, msg = "number_repeated_in_file", fmt.Sprintf("%s appears more than once in this file", r.number)
			}
			seen[key] = true
			if code != "" {
				d := msg
				bad = append(bad, struct {
					Code   string  `json:"code"`
					Detail *string `json:"detail,omitempty"`
					Line   int     `json:"line"`
				}{code, &d, r.line})
			}
			rows = append(rows, r)
		}
		if len(bad) > 0 {
			out = RegisterMembers422JSONResponse{Code: "rows_refused", Message: fmt.Sprintf("%d row(s) refused; nothing was registered.", len(bad)), Rows: bad}
			return errRegisterRefused
		}
		// The step-up, only now that something is about to be written, inside
		// this transaction: a refusal above never touched it.
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('member_register', $1, $2) RETURNING id::text`,
			caller, b.PinProofId.String()).Scan(&act); err != nil {
			if stepUpRefused[refusedBy(err)] {
				out = RegisterMembers403JSONResponse{Code: "step_up_required", Message: "register with a fresh PIN proof for member_register, your own"}
				return errRegisterRefused
			}
			return err
		}
		var batch string
		if err := x.QueryRow(ctx, `INSERT INTO member_register_batches (society_id, reference, act_id, row_count) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			society, ref, act, len(rows)).Scan(&batch); err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := registerOne(ctx, x, society, act, batch, r); err != nil {
				return err
			}
		}
		rec, _, err := registeredRecord(ctx, x, society, ref)
		out = RegisterMembers201JSONResponse(rec)
		return err
	})
	if errors.Is(err, errRegisterRefused) {
		return out, nil
	}
	if err != nil {
		s.log.Error("register import failed; nothing was registered", "society", society, "reference", ref, "err", err)
		return RegisterMembers500JSONResponse{Code: "register_failed", Message: "Nothing was registered. Retrying with the same reference is safe: it has not been used."}, nil
	}
	return out, nil
}

// registeredRecord: what the commit under this reference did, read from the
// members it registered, never from a request.
func registeredRecord(ctx context.Context, q querier, society, ref string) (RegisteredRecord, bool, error) {
	var rec RegisteredRecord
	var batch string
	err := q.QueryRow(ctx, `SELECT id::text, registered_at FROM member_register_batches WHERE society_id = $1 AND reference = $2`, society, ref).Scan(&batch, &rec.RegisteredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return rec, false, nil
	}
	if err != nil {
		return rec, false, err
	}
	rec.Reference = ref
	rows, err := q.Query(ctx, `SELECT coalesce(register_line, 0), id::text, member_number FROM members WHERE register_batch_id = $1 ORDER BY register_line, member_number`, batch)
	if err != nil {
		return rec, false, err
	}
	defer rows.Close()
	rec.Members = []struct {
		Line         int                `json:"line"`
		MemberId     openapi_types.UUID `json:"memberId"`
		MemberNumber string             `json:"memberNumber"`
	}{}
	for rows.Next() {
		var line int
		var id, num string
		if err := rows.Scan(&line, &id, &num); err != nil {
			return rec, false, err
		}
		rec.Members = append(rec.Members, struct {
			Line         int                `json:"line"`
			MemberId     openapi_types.UUID `json:"memberId"`
			MemberNumber string             `json:"memberNumber"`
		}{line, uuidOf(id), num})
	}
	return rec, true, rows.Err()
}
