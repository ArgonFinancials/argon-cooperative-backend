//go:build tier1

// THE MUTATION VEHICLE FOR THE REGISTER ENDPOINTS — test code, NOT the API. A
// reference implementation of GET/POST /societies/{s}/members, POST
// …/members/batches and GET …/branches (2026-11-10), breakable one named way
// per run (ARGON_TEST_API_FAKE=<break>):
//
//	members_any_reader          a member with no post reads the register
//	members_phone_unnormalized  a phone typed 0803… finds nobody stored as +234803…
//	members_enrolment_blind     enrolment always reads "none" (a signatory's half-issued code unseen)
//	members_balance_blind       openingBalancePosted always false (the treasurer re-posts)
//	register_phone_raw          the phone is not put in +234 form before registering (0803… refused)
//	register_refusal_as_500     a refused member answers 500, not 422 with why
//	import_partial              refused rows are skipped and the rest registered (not all or nothing)
//	import_first_refusal_only   only the first refused row is reported (the secretary fixes one line at a time)
//	import_key_ignored          a repeat of a committed reference registers again
//	branches_any_reader         anyone reads a society's branches
package tier1

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

var e164Ref = regexp.MustCompile(`^\+234[0-9]{10}$`)

// normalPhone: 0803…, 234803… and +234803… (spaces and dashes ignored) as +234 E.164.
func normalPhone(p string) string {
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

func (f *fakeAPI) officerOf(ctx context.Context, user, society string) bool {
	var ok bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))`, user, society).Scan(&ok)
	return ok
}

func (f *fakeAPI) summary(ctx context.Context, q querier, member string) (api.MemberSummary, error) {
	var s api.MemberSummary
	var id, branch, status, enrol string
	var joined time.Time
	err := q.QueryRow(ctx, `SELECT m.id::text, m.member_number, m.full_name, m.phone, b.code, m.status::text, m.joined_on,
		  EXISTS (SELECT 1 FROM auth_credentials c WHERE c.user_id = m.user_id),
		  EXISTS (SELECT 1 FROM auth_devices d WHERE d.user_id = m.user_id AND d.revoked_at IS NULL),
		  CASE WHEN EXISTS (SELECT 1 FROM auth_devices d WHERE d.user_id = m.user_id AND d.revoked_at IS NULL) THEN 'enrolled'
		       WHEN EXISTS (SELECT 1 FROM enrolment_codes c WHERE c.member_id = m.id AND c.used_at IS NULL AND c.voided_at IS NULL AND c.expires_at > now() AND c.code_digest IS NOT NULL) THEN 'code_live'
		       WHEN EXISTS (SELECT 1 FROM enrolment_codes c WHERE c.member_id = m.id AND c.used_at IS NULL AND c.voided_at IS NULL AND c.expires_at > now() AND c.code_digest IS NULL) THEN 'code_pending'
		       ELSE 'none' END,
		  EXISTS (SELECT 1 FROM ledger_entries e JOIN ledger_lines l ON l.entry_id = e.id JOIN accounts a ON a.id = l.account_id
		           WHERE e.kind = 'opening_balance' AND e.reverses_id IS NULL AND l.member_id = m.id AND a.role = 'member_savings'
		             AND NOT EXISTS (SELECT 1 FROM ledger_entries r WHERE r.reverses_id = e.id))
		FROM members m JOIN branches b ON b.id = m.branch_id WHERE m.id = $1`, member).
		Scan(&id, &s.MemberNumber, &s.FullName, &s.Phone, &branch, &status, &joined, &s.HasPin, &s.HasTrustedDevice, &enrol, &s.OpeningBalancePosted)
	if err != nil {
		return s, err
	}
	if f.brk == "members_enrolment_blind" {
		enrol = "none"
	}
	if f.brk == "members_balance_blind" {
		s.OpeningBalancePosted = false
	}
	s.MemberId, s.BranchCode, s.Status = uuidOf(id), branch, api.MemberSummaryStatus(status)
	s.JoinedOn = openapi_types.Date{Time: joined}
	s.Enrolment = api.MemberSummaryEnrolment(enrol)
	rows, err := q.Query(ctx, `SELECT a.post::text, a.is_signatory FROM appointments a WHERE a.member_id = $1
		AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date) ORDER BY a.post`, member)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	s.Posts = []struct {
		IsSignatory bool                       `json:"isSignatory"`
		Post        api.MemberSummaryPostsPost `json:"post"`
	}{}
	for rows.Next() {
		var post string
		var sig bool
		if err := rows.Scan(&post, &sig); err != nil {
			return s, err
		}
		s.Posts = append(s.Posts, struct {
			IsSignatory bool                       `json:"isSignatory"`
			Post        api.MemberSummaryPostsPost `json:"post"`
		}{sig, api.MemberSummaryPostsPost(post)})
	}
	return s, rows.Err()
}

func (f *fakeAPI) FindMembers(ctx context.Context, req api.FindMembersRequestObject) (api.FindMembersResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	if !f.officerOf(ctx, caller, society) && f.brk != "members_any_reader" {
		return api.FindMembers404JSONResponse{Code: "not_found", Message: "no such society"}, nil
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
	phone := normalPhone(q)
	if f.brk == "members_phone_unnormalized" {
		phone = q
	}
	rows, err := f.db.Query(ctx, `SELECT id::text FROM members WHERE society_id = $1
		  AND ($2 = '' OR lower(member_number) = lower($2) OR phone = $3 OR full_name ILIKE '%' || $2 || '%')
		  AND ($4 = '' OR member_number > $4)
		ORDER BY member_number LIMIT $5`, society, q, phone, after, limit+1)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	page := api.MemberPage{Items: []api.MemberSummary{}}
	for i, id := range ids {
		if i == limit {
			n := page.Items[limit-1].MemberNumber
			page.Next = &n
			break
		}
		s, err := f.summary(ctx, f.db, id)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, s)
	}
	return api.FindMembers200JSONResponse(page), nil
}

func (f *fakeAPI) ListBranches(ctx context.Context, req api.ListBranchesRequestObject) (api.ListBranchesResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	if !f.officerOf(ctx, caller, req.SocietyId.String()) && f.brk != "branches_any_reader" {
		return api.ListBranches404JSONResponse{Code: "not_found", Message: "no such society"}, nil
	}
	rows, err := f.db.Query(ctx, `SELECT code, name FROM branches WHERE society_id = $1 ORDER BY code`, req.SocietyId.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := api.ListBranches200JSONResponse{Items: []struct {
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

// regRow: one member to register, from a row or the one-member body.
type regRow struct {
	line                                 int
	number, name, phone, branch, channel string
	joined                               time.Time
	nokName, nokPhone, nokRel            string
}

// rowCodes: every reason this row is refused, before anything is written.
func (f *fakeAPI) rowCode(ctx context.Context, q querier, society string, r regRow) (string, string) {
	var branch, taken bool
	q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM branches WHERE society_id = $1 AND code = $2),
		EXISTS (SELECT 1 FROM members WHERE society_id = $1 AND member_number = $3)`, society, r.branch, r.number).Scan(&branch, &taken)
	nok := 0
	for _, v := range []string{r.nokName, r.nokPhone, r.nokRel} {
		if strings.TrimSpace(v) != "" {
			nok++
		}
	}
	switch {
	case strings.TrimSpace(r.name) == "":
		return "name_missing", "a member is registered by name"
	case !e164Ref.MatchString(r.phone):
		return "phone_not_nigerian", fmt.Sprintf("%q is not a Nigerian number", r.phone)
	case !branch:
		return "branch_unknown", fmt.Sprintf("this society has no branch %q", r.branch)
	case r.joined.After(time.Now()):
		return "joined_in_future", "joined on a day that has not come"
	case nok != 0 && (nok != 3 || !e164Ref.MatchString(r.nokPhone)):
		return "next_of_kin_incomplete", "a next of kin is name, phone and relationship, or nothing"
	case taken:
		return "number_already_registered", fmt.Sprintf("%s is already a member of this society", r.number)
	}
	return "", ""
}

func (f *fakeAPI) phoneOf(p string) string {
	if f.brk == "register_phone_raw" {
		return strings.TrimSpace(p)
	}
	return normalPhone(p)
}

func nokOf(n *api.NextOfKin) (string, string, string) {
	if n == nil {
		return "", "", ""
	}
	return n.FullName, n.Phone, n.Relationship
}

func (f *fakeAPI) register(ctx context.Context, x pgx.Tx, society, act string, batch any, r regRow) (string, error) {
	var id string
	err := x.QueryRow(ctx, `SELECT register_member($1, $2, $3, $4, $5, $6::date, $7::collection_channel, nullif($8, ''), nullif($9, ''), nullif($10, ''), $11, $12, $13)::text`,
		society, r.branch, r.number, r.name, r.phone, r.joined.Format("2006-01-02"), r.channel, r.nokName, r.nokPhone, r.nokRel, act, batch, nilIfZero(r.line)).Scan(&id)
	return id, err
}

func nilIfZero(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func (f *fakeAPI) secretaryOf(ctx context.Context, user, society string) bool {
	var ok bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND a.post = 'secretary'
		  AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))`, user, society).Scan(&ok)
	return ok
}

func (f *fakeAPI) RegisterMember(ctx context.Context, req api.RegisterMemberRequestObject) (api.RegisterMemberResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	if !f.officerOf(ctx, caller, society) {
		return api.RegisterMember404JSONResponse{Code: "not_found", Message: "no such society"}, nil
	}
	if !f.secretaryOf(ctx, caller, society) {
		return api.RegisterMember403JSONResponse{Code: "not_permitted", Message: "registering members is the secretary's"}, nil
	}
	b := req.Body
	nn, np, nr := nokOf(b.NextOfKin)
	r := regRow{number: strings.TrimSpace(b.MemberNumber), name: b.FullName, phone: f.phoneOf(b.Phone), branch: b.BranchCode,
		channel: string(b.CollectionChannel), joined: b.JoinedOn.Time, nokName: nn, nokPhone: normalPhone(np), nokRel: nr}
	if code, msg := f.rowCode(ctx, f.db, society, r); code != "" && f.brk != "register_phone_raw" {
		if f.brk == "register_refusal_as_500" {
			return nil, errors.New(msg)
		}
		return api.RegisterMember422JSONResponse{Code: api.RegisterRefusalCode(code), Message: msg, Rows: []struct {
			Code   string  `json:"code"`
			Detail *string `json:"detail,omitempty"`
			Line   int     `json:"line"`
		}{}}, nil
	}
	var id string
	err := f.actThen(ctx, caller, "member_register", b.PinProofId.String(), false, func(x pgx.Tx, act *string) error {
		var err error
		id, err = f.register(ctx, x, society, *act, nil, r)
		return err
	})
	if err != nil {
		c := pgConstraint(err)
		if stepUpRefusals[c] {
			return api.RegisterMember403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for member_register, your own"}, nil
		}
		if c == "member_phone_e164" {
			return api.RegisterMember422JSONResponse{Code: "phone_not_nigerian", Message: dbMessage(err), Rows: []struct {
				Code   string  `json:"code"`
				Detail *string `json:"detail,omitempty"`
				Line   int     `json:"line"`
			}{}}, nil
		}
		return nil, err
	}
	s, err := f.summary(ctx, f.db, id)
	if err != nil {
		return nil, err
	}
	return api.RegisterMember201JSONResponse(s), nil
}

func (f *fakeAPI) RegisterMembers(ctx context.Context, req api.RegisterMembersRequestObject) (api.RegisterMembersResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	ref := req.Params.IdempotencyKey
	if !f.officerOf(ctx, caller, society) {
		return api.RegisterMembers404JSONResponse{Code: "not_found", Message: "no such society"}, nil
	}
	if !f.secretaryOf(ctx, caller, society) {
		return api.RegisterMembers403JSONResponse{Code: "not_permitted", Message: "registering members is the secretary's"}, nil
	}
	b := req.Body
	var out api.RegisterMembersResponseObject
	errRefused := errors.New("refused")
	err := pgx.BeginFunc(ctx, f.db, func(x pgx.Tx) error {
		if _, err := x.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('argon.register:' || $1))`, society); err != nil {
			return err
		}
		if f.brk != "import_key_ignored" {
			if rec, ok, err := f.registeredRecord(ctx, x, society, ref); err != nil || ok {
				if ok {
					out = api.RegisterMembers200JSONResponse(rec)
				}
				return err
			}
		}
		if len(b.Rows) == 0 {
			out = api.RegisterMembers422JSONResponse{Code: "nothing_to_register", Message: "no row to register", Rows: refusedRows(nil)}
			return errRefused
		}
		var rows []regRow
		var bad []refusedRow
		seen := map[string]bool{}
		for _, row := range b.Rows {
			nn, np, nr := nokOf(row.NextOfKin)
			r := regRow{line: row.Line, number: strings.TrimSpace(row.MemberNumber), name: row.FullName, phone: normalPhone(row.Phone), branch: row.BranchCode,
				channel: string(row.CollectionChannel), joined: row.JoinedOn.Time, nokName: nn, nokPhone: normalPhone(np), nokRel: nr}
			code, msg := f.rowCode(ctx, x, society, r)
			if code == "" && seen[strings.ToLower(r.number)] {
				code, msg = "number_repeated_in_file", fmt.Sprintf("%s appears more than once in this file", r.number)
			}
			seen[strings.ToLower(r.number)] = true
			if code != "" {
				bad = append(bad, refusedRow{r.line, code, msg})
				if f.brk == "import_partial" {
					continue
				}
			}
			rows = append(rows, r)
		}
		if len(bad) > 0 && f.brk != "import_partial" {
			if f.brk == "import_first_refusal_only" {
				bad = bad[:1]
			}
			out = api.RegisterMembers422JSONResponse{Code: "rows_refused", Message: fmt.Sprintf("%d row(s) refused; nothing was registered", len(bad)), Rows: refusedRows(bad)}
			return errRefused
		}
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('member_register', $1, $2) RETURNING id::text`,
			caller, b.PinProofId.String()).Scan(&act); err != nil {
			if stepUpRefusals[pgConstraint(err)] {
				out = api.RegisterMembers403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for member_register, your own"}
				return errRefused
			}
			return err
		}
		var batch string
		if err := x.QueryRow(ctx, `INSERT INTO member_register_batches (society_id, reference, act_id, row_count) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			society, ref, act, len(rows)).Scan(&batch); err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := f.register(ctx, x, society, act, batch, r); err != nil {
				return err
			}
		}
		rec, _, err := f.registeredRecord(ctx, x, society, ref)
		out = api.RegisterMembers201JSONResponse(rec)
		return err
	})
	if errors.Is(err, errRefused) {
		return out, nil
	}
	if err != nil {
		return api.RegisterMembers500JSONResponse{Code: "register_failed", Message: "Nothing was registered. Retrying with the same reference is safe."}, nil
	}
	return out, nil
}

type refusedRow struct {
	line       int
	code, what string
}

func refusedRows(bad []refusedRow) []struct {
	Code   string  `json:"code"`
	Detail *string `json:"detail,omitempty"`
	Line   int     `json:"line"`
} {
	out := []struct {
		Code   string  `json:"code"`
		Detail *string `json:"detail,omitempty"`
		Line   int     `json:"line"`
	}{}
	for _, b := range bad {
		d := b.what
		out = append(out, struct {
			Code   string  `json:"code"`
			Detail *string `json:"detail,omitempty"`
			Line   int     `json:"line"`
		}{b.code, &d, b.line})
	}
	return out
}

// registeredRecord: what the commit under this reference did, read from the
// members it registered.
func (f *fakeAPI) registeredRecord(ctx context.Context, q querier, society, ref string) (api.RegisteredRecord, bool, error) {
	var rec api.RegisteredRecord
	var batch string
	err := q.QueryRow(ctx, `SELECT id::text, registered_at FROM member_register_batches WHERE society_id = $1 AND reference = $2`, society, ref).Scan(&batch, &rec.RegisteredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return rec, false, nil
	}
	if err != nil {
		return rec, false, err
	}
	rec.Reference = ref
	rows, err := q.Query(ctx, `SELECT coalesce(register_line, 0), id::text, member_number FROM members WHERE register_batch_id = $1 ORDER BY register_line`, batch)
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
