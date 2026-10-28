package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// THE MONEY ENDPOINTS (0011, 0012, 0015). Written against the approved
// acceptance tests, test/tier1/tier1_api_money_test.go, after 0015, so no
// endpoint that could authorise a self-payout ever existed.
//
//   - RECORDING AND AUTHORISING ARE DIFFERENT DUTIES (the role matrix):
//     recording money is the treasurer's or the secretary's; authorising a
//     release (the hold, and releasing it) a signatory's. An officer here
//     without the duty: 403 not_permitted. Anyone else: 404, as for nothing
//     there. These are the API's own rules.
//   - Everything else is the DATABASE's (0011–0015): the step-up, the charges,
//     withdrawable, nobody authorising or attesting to what they benefit from,
//     the authoriser never recording, one client reference once. Every write is
//     the caller's act and its row in ONE transaction; each refusal is mapped to
//     what the person can do, carrying the database's words where they explain.
//   - THE SERVER BUILDS THE ENTRY: lines by role, charges from the series (a
//     contribution, or a payout with no hold) or the hold. A client never sends
//     a charge.
//   - THE LEDGER RECORDS WHAT HAPPENED: a contribution the series cannot charge
//     is recorded uncharged (charges null, never zeros); a payout that differs
//     from its hold is recorded, and the flags come back in the answer.
//   - A contribution's Idempotency-Key is its client reference (0015): a
//     repeat answers 200 with the FIRST record, writes nothing, and needs no
//     step-up.

var (
	moneyRecorders   = []string{"treasurer", "secretary"}
	moneyAuthorisers = []string{"chairman", "treasurer", "secretary"}
	postLabels       = map[string]string{"chairman": "Chairman", "treasurer": "Treasurer", "secretary": "Secretary",
		"internal_auditor": "Internal auditor", "branch_officer": "Branch officer", "loan_officer": "Loan officer"}

	notPermittedRecord    = Problem{Code: "not_permitted", Message: "recording money is the treasurer's or the secretary's"}
	notPermittedAuthorise = Problem{Code: "not_permitted", Message: "authorising or releasing a payout is a signatory's"}
	conflictRecord        = Problem{Code: "conflict_of_interest", Message: "nobody attests to a movement they benefit from: this is your own savings. Another officer records it"}
	conflictAuthorise     = Problem{Code: "conflict_of_interest", Message: "nobody authorises a movement they benefit from: this payout is from your own savings. Another signatory authorises it"}
	conflictRelease       = Problem{Code: "conflict_of_interest", Message: "nobody releases the hold on their own payout: a release frees your own withdrawable. Another signatory releases it"}
	dutiesSeparated       = Problem{Code: "duties_separated", Message: "you authorised this payout's hold: authorising and attesting are different duties. Another officer records it"}

	// The step-up did not hold, at the act (0005) or at the entry or hold (0011, 0012).
	moneyStepUpRefused = map[string]bool{"act_needs_pin_proof": true, "act_proof_for_this_act": true, "proof_minted_elsewhere": true,
		"entry_needs_step_up": true, "entry_act_is_actors": true, "entry_act_kind": true, "entry_act_same_transaction": true,
		"hold_needs_step_up": true, "hold_act_is_holders": true, "hold_act_kind": true, "hold_act_same_transaction": true}
	notConfigured = map[string]bool{"account_floor_unset": true, "charge_partner_fee_unset": true, "charge_vat_unset": true,
		"charge_levy_unset": true, "charge_margin_unset": true, "charge_band_missing": true}
)

func stepUpProblem(act string) Problem {
	return Problem{Code: "step_up_required", Message: "a fresh PIN proof for " + act + ", your own"}
}

func notFound(what string) Problem { return Problem{Code: "not_found", Message: "no such " + what} }

// dbMessage: the database's own words for a refusal.
func dbMessage(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Message
	}
	return err.Error()
}

// officerHere: a current post in the society (the auditor included).
func (s *server) officerHere(ctx context.Context, society string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT person_holds_post($1, $2)`, callerOf(ctx), society).Scan(&ok)
	return ok, err
}

// dutyPost: the caller's current post in the society among `posts`, in that
// order of preference; "" if none.
func (s *server) dutyPost(ctx context.Context, society string, posts []string) (string, error) {
	var post string
	err := s.db.QueryRow(ctx, `SELECT a.post::text FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)
		  AND a.post::text = ANY($3) ORDER BY array_position($3, a.post::text) LIMIT 1`, callerOf(ctx), society, posts).Scan(&post)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return post, err
}

func (s *server) memberHere(ctx context.Context, member, society string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2)`, member, society).Scan(&ok)
	return ok, err
}

func (s *server) societyExists(ctx context.Context, society string) bool {
	var ok bool
	s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM societies WHERE id = $1)`, society).Scan(&ok)
	return ok
}

type charges struct{ fee, vat, levy, margin int64 }

func (c charges) total() int64 { return c.fee + c.vat + c.levy + c.margin }

func (c charges) wire() Charges {
	return Charges{PartnerFeeKobo: magnitudeOf(c.fee), VatKobo: magnitudeOf(c.vat), StatutoryLevyKobo: magnitudeOf(c.levy),
		ArgonMarginKobo: magnitudeOf(c.margin), TotalKobo: magnitudeOf(c.total())}
}

// quote: the series' charge on the date; determined false where the series
// cannot say (a component or band not set): UNDETERMINED, not zero.
func (s *server) quote(ctx context.Context, society, kind string, amount int64, on string) (c charges, determined bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT * FROM charge_quote($1, $2, $3, $4)`, society, kind, amount, on).Scan(&c.fee, &c.vat, &c.levy, &c.margin)
	if err != nil {
		if strings.HasPrefix(refusedBy(err), "charge_") {
			return charges{}, false, nil
		}
		return charges{}, false, err
	}
	return c, true, nil
}

type moneyLine struct {
	role, chargeRole, member string
	kobo                     int64
}

// chargeLines: each component on its own line and account (a zero line is
// never written).
func chargeLines(c charges) []moneyLine {
	var ls []moneyLine
	for _, x := range []struct {
		kobo             int64
		acctRole, charge string
	}{{c.fee, "charges_partner", "partner_charge"}, {c.vat, "charges_vat", "vat"}, {c.levy, "statutory_levy_payable", "statutory_levy"}, {c.margin, "charges_margin", "argon_margin"}} {
		if x.kobo != 0 {
			ls = append(ls, moneyLine{role: x.acctRole, chargeRole: x.charge, kobo: -x.kobo})
		}
	}
	return ls
}

type moneyEntry struct {
	society, kind, valueDate, post string
	request, hold, ref, clientRef  *string
	lines                          []moneyLine
}

// postMoney: the caller's act of `act` and the entry with its lines, in ONE
// transaction. Accounts are found by role, in the society.
func (s *server) postMoney(ctx context.Context, act, proof string, e moneyEntry) (string, error) {
	var id string
	err := s.onStepUp(ctx, act, proof, func(x pgx.Tx, actID string) error {
		if err := x.QueryRow(ctx, `INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label,
			  act_id, request_id, hold_id, payout_reference, client_reference) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id::text`,
			e.society, e.kind, e.valueDate, callerOf(ctx), e.post, postLabels[e.post], actID, e.request, e.hold, e.ref, e.clientRef).Scan(&id); err != nil {
			return err
		}
		for _, l := range e.lines {
			var member *string
			if l.member != "" {
				member = &l.member
			}
			chargeRole := l.chargeRole
			if chargeRole == "" {
				chargeRole = "principal"
			}
			tag, err := x.Exec(ctx, `INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id)
				SELECT $1, a.id, $3, $4, $5 FROM accounts a WHERE a.society_id = $2 AND a.role = $6 ORDER BY a.code LIMIT 1`,
				id, e.society, l.kobo, chargeRole, member, l.role)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return errNoAccount{role: l.role}
			}
		}
		return nil
	})
	return id, err
}

// errNoAccount: the society's chart has no account of a role this movement needs.
type errNoAccount struct{ role string }

func (e errNoAccount) Error() string { return "this society's chart has no " + e.role + " account" }

// movementOf: a recorded movement, read back from the ledger: what was
// written, never what was asked.
func (s *server) movementOf(ctx context.Context, id string) (MoneyMovement, error) {
	var m MoneyMovement
	var kind, society, actor, post, member string
	var date time.Time
	var hold, ref *string
	var amount int64
	var c charges
	var undetermined bool
	err := s.db.QueryRow(ctx, `SELECT e.kind::text, e.society_id::text, e.value_date, e.actor_user_id::text, coalesce(e.actor_post::text, ''),
		  e.hold_id::text, e.payout_reference,
		  (SELECT coalesce(abs(sum(l.amount_kobo)), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = e.id AND a.role = 'bank'),
		  (SELECT l.member_id::text FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = e.id AND a.role = 'member_savings' LIMIT 1),
		  coalesce((SELECT -sum(amount_kobo) FROM ledger_lines WHERE entry_id = e.id AND charge_role = 'partner_charge'), 0),
		  coalesce((SELECT -sum(amount_kobo) FROM ledger_lines WHERE entry_id = e.id AND charge_role = 'vat'), 0),
		  coalesce((SELECT -sum(amount_kobo) FROM ledger_lines WHERE entry_id = e.id AND charge_role = 'statutory_levy'), 0),
		  coalesce((SELECT -sum(amount_kobo) FROM ledger_lines WHERE entry_id = e.id AND charge_role = 'argon_margin'), 0),
		  EXISTS (SELECT 1 FROM ledger_discrepancies d WHERE d.entry_id = e.id AND d.kind = 'charge_undetermined')
		FROM ledger_entries e WHERE e.id = $1`, id).
		Scan(&kind, &society, &date, &actor, &post, &hold, &ref, &amount, &member, &c.fee, &c.vat, &c.levy, &c.margin, &undetermined)
	if err != nil {
		return m, err
	}
	m = MoneyMovement{EntryId: uuidOf(id), Kind: MoneyMovementKind(kind), SocietyId: uuidOf(society), MemberId: uuidOf(member),
		AmountKobo: magnitudeOf(amount), ValueDate: openapi_types.Date{Time: date}, ChargeDetermined: !undetermined,
		HoldId: uuidPtr(hold), PayoutReference: ref}
	if !undetermined {
		w := c.wire()
		m.Charges = &w
	}
	moved := amount + c.total()
	if kind == "contribution_in" {
		moved = amount - c.total()
	}
	m.MemberAmountKobo = magnitudeOf(moved)
	p := PostedByPost(post)
	m.PostedBy = PostedBy{UserId: uuidOf(actor), Post: &p, PostLabel: postLabels[post]}
	m.Flags = []Discrepancy{}
	rows, err := s.db.Query(ctx, `SELECT kind, expected, observed FROM ledger_discrepancies WHERE entry_id = $1 ORDER BY kind, raised_at`, id)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Discrepancy
		var k string
		if err := rows.Scan(&k, &d.Expected, &d.Observed); err != nil {
			return m, err
		}
		d.Kind = DiscrepancyKind(k)
		m.Flags = append(m.Flags, d)
	}
	return m, rows.Err()
}

func magnitudeOk(v Magnitude) (int64, bool) {
	n, err := strconv.ParseInt(string(v), 10, 64)
	return n, err == nil && n >= 0
}

// =============================================================================
// CONTRIBUTIONS
// =============================================================================

// contributionByKey: the entry already recorded under this key in the society.
func (s *server) contributionByKey(ctx context.Context, society, key string) (string, error) {
	var id string
	err := s.db.QueryRow(ctx, `SELECT id::text FROM ledger_entries WHERE society_id = $1 AND client_reference = $2`, society, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *server) RecordContribution(ctx context.Context, req RecordContributionRequestObject) (RecordContributionResponseObject, error) {
	society := req.SocietyId.String()
	officer, err := s.officerHere(ctx, society)
	if err != nil {
		return nil, err
	}
	if !officer {
		return RecordContribution404JSONResponse(notFound("society")), nil
	}
	post, err := s.dutyPost(ctx, society, moneyRecorders)
	if err != nil {
		return nil, err
	}
	if post == "" {
		return RecordContribution403JSONResponse(notPermittedRecord), nil
	}
	b := req.Body
	if b == nil {
		return RecordContribution400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	// Already recorded under this key: nothing is written and no step-up is
	// needed, because nothing attributable happens (postAll's order).
	key := req.Params.IdempotencyKey
	if first, err := s.contributionByKey(ctx, society, key); err != nil || first != "" {
		if err != nil {
			return nil, err
		}
		m, err := s.movementOf(ctx, first)
		if err != nil {
			return nil, err
		}
		return RecordContribution200JSONResponse(m), nil
	}
	amount, ok := magnitudeOk(b.AmountKobo)
	if !ok {
		return RecordContribution400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	if amount == 0 {
		return RecordContribution422JSONResponse{Code: "zero_amount", Message: "a contribution of nothing is not recorded"}, nil
	}
	member := b.MemberId.String()
	if here, err := s.memberHere(ctx, member, society); err != nil || !here {
		if err != nil {
			return nil, err
		}
		return RecordContribution422JSONResponse{Code: "unknown_member", Message: "not a member of this society"}, nil
	}
	date := b.ValueDate.Format("2006-01-02")
	c, _, err := s.quote(ctx, society, "contribution_in", amount, date)
	if err != nil {
		return nil, err
	}
	// Undetermined: recorded uncharged; the database flags it (0012).
	lines := []moneyLine{{role: "bank", kobo: amount}, {role: "member_savings", kobo: -(amount - c.total()), member: member}}
	lines = append(lines, chargeLines(c)...)
	entry, err := s.postMoney(ctx, "contribution_record", b.PinProofId.String(),
		moneyEntry{society: society, kind: "contribution_in", valueDate: date, post: post, clientRef: &key, lines: lines})
	if err != nil {
		switch c := refusedBy(err); {
		case moneyStepUpRefused[c]:
			return RecordContribution403JSONResponse(stepUpProblem("contribution_record")), nil
		case c == "contribution_not_recorded_by_beneficiary":
			return RecordContribution403JSONResponse(conflictRecord), nil
		case c == "contribution_recorded_once": // a retry that raced the first
			first, ferr := s.contributionByKey(ctx, society, key)
			if ferr == nil && first != "" {
				m, merr := s.movementOf(ctx, first)
				if merr == nil {
					return RecordContribution200JSONResponse(m), nil
				}
			}
		}
		return nil, err
	}
	m, err := s.movementOf(ctx, entry)
	if err != nil {
		return nil, err
	}
	return RecordContribution201JSONResponse(m), nil
}

// =============================================================================
// HOLDS: AUTHORISED TO EXECUTE
// =============================================================================

func (s *server) holdView(ctx context.Context, id string) (PayoutHold, error) {
	var h PayoutHold
	var hid, req, member, by string
	var amount int64
	var c charges
	var paid, released bool
	var reason *string
	err := s.db.QueryRow(ctx, `SELECT h.id::text, h.request_id::text, h.member_id::text, h.amount_kobo, h.partner_fee_kobo, h.vat_kobo,
		  h.statutory_levy_kobo, h.argon_margin_kobo, h.actor_user_id::text, h.held_at,
		  EXISTS (SELECT 1 FROM ledger_entries e WHERE e.hold_id = h.id), r.hold_id IS NOT NULL, r.reason
		FROM withdrawal_holds h LEFT JOIN withdrawal_hold_releases r ON r.hold_id = h.id WHERE h.id = $1`, id).
		Scan(&hid, &req, &member, &amount, &c.fee, &c.vat, &c.levy, &c.margin, &by, &h.AuthorisedAt, &paid, &released, &reason)
	if err != nil {
		return h, err
	}
	h.HoldId, h.RequestId, h.MemberId, h.AuthorisedBy = uuidOf(hid), uuidOf(req), uuidOf(member), uuidOf(by)
	h.AmountKobo, h.Charges, h.TotalKobo = magnitudeOf(amount), c.wire(), magnitudeOf(amount+c.total())
	h.State = PayoutHoldStateOpen
	switch {
	case paid:
		h.State = PayoutHoldStatePaid
	case released:
		h.State = PayoutHoldStateReleased
	}
	h.ReleaseReason = reason
	return h, nil
}

func (s *server) AuthorisePayout(ctx context.Context, req AuthorisePayoutRequestObject) (AuthorisePayoutResponseObject, error) {
	r, officer, err := s.openForOfficer(ctx, req.RequestId.String())
	if err != nil {
		return nil, err
	}
	if !officer {
		return AuthorisePayout404JSONResponse(notFound("request")), nil
	}
	if post, err := s.dutyPost(ctx, r.society, moneyAuthorisers); err != nil || post == "" {
		if err != nil {
			return nil, err
		}
		return AuthorisePayout403JSONResponse(notPermittedAuthorise), nil
	}
	if req.Body == nil {
		return AuthorisePayout400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var hold string
	err = s.onStepUp(ctx, "withdrawal_authorisation", req.Body.PinProofId.String(), func(x pgx.Tx, act string) error {
		// The charges are the database's to fix (0012); the app cannot write them.
		return x.QueryRow(ctx, `INSERT INTO withdrawal_holds (request_id, society_id, member_id, amount_kobo, actor_user_id, act_id)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`, r.id, r.society, r.member, r.amount, callerOf(ctx), act).Scan(&hold)
	})
	if err != nil {
		switch c := refusedBy(err); {
		case moneyStepUpRefused[c]:
			return AuthorisePayout403JSONResponse(stepUpProblem("withdrawal_authorisation")), nil
		case c == "authoriser_not_beneficiary":
			return AuthorisePayout403JSONResponse(conflictAuthorise), nil
		case c == "hold_request_approved" || c == "hold_matches_request":
			return AuthorisePayout409JSONResponse{Code: "not_approved", Message: dbMessage(err)}, nil
		case c == "hold_once_open":
			return AuthorisePayout409JSONResponse{Code: "hold_open", Message: dbMessage(err)}, nil
		case c == "withdrawal_within_withdrawable":
			why, werr := s.holdRefusal(ctx, r.id, dbMessage(err))
			if werr != nil {
				return nil, werr
			}
			return AuthorisePayout422JSONResponse(why), nil
		case notConfigured[c]:
			return AuthorisePayout422JSONResponse{Code: "not_configured", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	h, err := s.holdView(ctx, hold)
	if err != nil {
		return nil, err
	}
	return AuthorisePayout201JSONResponse(h), nil
}

// holdRefusal: WHAT CHANGED, for a hold the database refused because it no
// longer fits (balance handlers piece 4, 2026-10-28). The refusal is the
// database's (the hold trigger, on member_position); this only explains it:
// the amount, the charge it was quoted, the charge now, what is needed, what
// can be withdrawn, and the causes, every figure and both causes worked out
// in the query.
//
// READ AFTER THE REFUSED TRANSACTION HAS ROLLED BACK, ON PURPOSE. The figures
// describe the moment of this read, not the instant of the refusal: if a
// payment were recorded in between, they would show the newer position. That
// gap is the honest one. Do NOT move this read inside the hold's transaction
// to close it: that transaction is being rolled back, and figures read from
// it describe a state that never existed. The refusal itself stands either
// way: it was the database's.
func (s *server) holdRefusal(ctx context.Context, request, dbWords string) (HoldRefusal, error) {
	out := HoldRefusal{Code: "exceeds_withdrawable", Message: dbWords}
	var amount, now, needed int64
	var quoted, figure *int64
	var changed, fell bool
	var since *time.Time
	err := s.db.QueryRow(ctx, `SELECT r.amount_kobo,
		       CASE WHEN r.quoted_on IS NULL THEN NULL ELSE r.quoted_partner_fee_kobo + r.quoted_vat_kobo + r.quoted_statutory_levy_kobo + r.quoted_argon_margin_kobo END,
		       q.t, p.withdrawable_kobo, r.amount_kobo + q.t,
		       coalesce(r.quoted_on IS NOT NULL AND q.t <> r.quoted_partner_fee_kobo + r.quoted_vat_kobo + r.quoted_statutory_levy_kobo + r.quoted_argon_margin_kobo, false),
		       coalesce(r.quoted_on IS NOT NULL AND p.withdrawable_kobo IS NOT NULL
		                AND r.amount_kobo + r.quoted_partner_fee_kobo + r.quoted_vat_kobo + r.quoted_statutory_levy_kobo + r.quoted_argon_margin_kobo > p.withdrawable_kobo, false),
		       (SELECT max(cs.effective_from) FROM charge_schedules cs
		         WHERE cs.society_id = r.society_id AND cs.kind = 'withdrawal_out' AND cs.effective_from <= current_date)
		  FROM approval_requests r,
		       LATERAL (SELECT c.partner_fee_kobo + c.vat_kobo + c.statutory_levy_kobo + c.argon_margin_kobo AS t
		                  FROM charge_quote(r.society_id, 'withdrawal_out', r.amount_kobo, current_date) c) q,
		       LATERAL member_position(r.member_id) p
		 WHERE r.id = $1`, request).Scan(&amount, &quoted, &now, &figure, &needed, &changed, &fell, &since)
	if err != nil || quoted == nil || figure == nil {
		return out, err // no quote kept (before 0017) or no figure: the database's own words
	}
	var causes []HoldRefusalCauses
	var why []string
	if changed {
		causes = append(causes, ChargeChanged)
		on := "today"
		if since != nil {
			on = "on " + since.Format("2 January 2006")
		}
		why = append(why, "The society's charges changed "+on+": the quoted "+naira(*quoted)+" is now "+naira(now)+".")
	}
	if fell {
		causes = append(causes, WithdrawableFell)
		why = append(why, "What the member can withdraw has fallen since the request was raised (another payout authorised, a guarantee, or the floor).")
	}
	a, q, n, nd, w := magnitudeOf(amount), magnitudeOf(*quoted), magnitudeOf(now), magnitudeOf(needed), magnitudeOf(*figure)
	out.AmountKobo, out.QuotedChargeKobo, out.ChargeNowKobo, out.NeededKobo, out.WithdrawableKobo, out.Causes = &a, &q, &n, &nd, &w, &causes
	// The sentence, then the database's own words: its figures stay in the
	// refusal as they always have (TestApiMoney15), beside the breakdown.
	out.Message = "This request no longer fits. " + strings.Join(why, " ") + " " + naira(needed) + " is needed and " + naira(*figure) + " can be withdrawn. (" + dbWords + ")"
	return out, nil
}

func (s *server) ReleaseHold(ctx context.Context, req ReleaseHoldRequestObject) (ReleaseHoldResponseObject, error) {
	var society string
	err := s.db.QueryRow(ctx, `SELECT society_id::text FROM withdrawal_holds WHERE id = $1`, req.HoldId.String()).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseHold404JSONResponse(notFound("hold")), nil
	}
	if err != nil {
		return nil, err
	}
	if officer, err := s.officerHere(ctx, society); err != nil || !officer {
		if err != nil {
			return nil, err
		}
		return ReleaseHold404JSONResponse(notFound("hold")), nil
	}
	if post, err := s.dutyPost(ctx, society, moneyAuthorisers); err != nil || post == "" {
		if err != nil {
			return nil, err
		}
		return ReleaseHold403JSONResponse(notPermittedAuthorise), nil
	}
	if req.Body == nil {
		return ReleaseHold400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if strings.TrimSpace(req.Body.Reason) == "" {
		return ReleaseHold422JSONResponse{Code: "reason_required", Message: "say why the transfer will not happen"}, nil
	}
	_, err = s.db.Exec(ctx, `INSERT INTO withdrawal_hold_releases (hold_id, actor_user_id, reason) VALUES ($1, $2, $3)`,
		req.HoldId.String(), callerOf(ctx), req.Body.Reason)
	if err != nil {
		switch c := refusedBy(err); {
		case c == "releaser_not_beneficiary":
			return ReleaseHold403JSONResponse(conflictRelease), nil
		case c == "hold_release_unpaid":
			return ReleaseHold409JSONResponse{Code: "hold_paid", Message: dbMessage(err)}, nil
		case c == "withdrawal_hold_releases_pkey":
			return ReleaseHold409JSONResponse{Code: "hold_released", Message: "this hold was already released"}, nil
		case c == "hold_release_names_reason":
			return ReleaseHold422JSONResponse{Code: "reason_required", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	h, err := s.holdView(ctx, req.HoldId.String())
	if err != nil {
		return nil, err
	}
	return ReleaseHold201JSONResponse(h), nil
}

// =============================================================================
// PAYOUTS: WHAT HAPPENED
// =============================================================================

func (s *server) RecordPayout(ctx context.Context, req RecordPayoutRequestObject) (RecordPayoutResponseObject, error) {
	society := req.SocietyId.String()
	officer, err := s.officerHere(ctx, society)
	if err != nil {
		return nil, err
	}
	if !officer {
		return RecordPayout404JSONResponse(notFound("society")), nil
	}
	post, err := s.dutyPost(ctx, society, moneyRecorders)
	if err != nil {
		return nil, err
	}
	if post == "" {
		return RecordPayout403JSONResponse(notPermittedRecord), nil
	}
	b := req.Body
	if b == nil {
		return RecordPayout400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	amount, ok := magnitudeOk(b.AmountKobo)
	if !ok {
		return RecordPayout400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	ref := strings.TrimSpace(b.PayoutReference)
	if ref == "" {
		return RecordPayout400JSONResponse{Code: "bad_request", Message: "a payout names the transfer that paid it"}, nil
	}
	if amount == 0 {
		return RecordPayout422JSONResponse{Code: "zero_amount", Message: "a payout of nothing is not recorded"}, nil
	}
	member := b.MemberId.String()
	if here, err := s.memberHere(ctx, member, society); err != nil || !here {
		if err != nil {
			return nil, err
		}
		return RecordPayout422JSONResponse{Code: "unknown_member", Message: "not a member of this society"}, nil
	}
	date := b.ValueDate.Format("2006-01-02")
	var c charges
	var hold, request *string
	if b.HoldId != nil {
		id := b.HoldId.String()
		var rq string
		err := s.db.QueryRow(ctx, `SELECT request_id::text, partner_fee_kobo, vat_kobo, statutory_levy_kobo, argon_margin_kobo
			FROM withdrawal_holds WHERE id = $1 AND society_id = $2`, id, society).Scan(&rq, &c.fee, &c.vat, &c.levy, &c.margin)
		if errors.Is(err, pgx.ErrNoRows) {
			return RecordPayout422JSONResponse{Code: "unknown_hold", Message: "not a hold of this society"}, nil
		}
		if err != nil {
			return nil, err
		}
		hold, request = &id, &rq // the charges are the hold's, fixed when it was authorised
	} else if c, _, err = s.quote(ctx, society, "withdrawal_out", amount, date); err != nil {
		return nil, err // no hold: the series' charge where it can say, otherwise none; flagged payout_without_hold either way
	}
	lines := []moneyLine{{role: "member_savings", kobo: amount + c.total(), member: member}, {role: "bank", kobo: -amount}}
	lines = append(lines, chargeLines(c)...)
	entry, err := s.postMoney(ctx, "withdrawal_payout", b.PinProofId.String(),
		moneyEntry{society: society, kind: "withdrawal_out", valueDate: date, post: post, request: request, hold: hold, ref: &ref, lines: lines})
	if err != nil {
		switch c := refusedBy(err); {
		case moneyStepUpRefused[c]:
			return RecordPayout403JSONResponse(stepUpProblem("withdrawal_payout")), nil
		case c == "payout_not_recorded_by_beneficiary":
			return RecordPayout403JSONResponse(conflictRecord), nil
		case c == "authoriser_never_records":
			return RecordPayout403JSONResponse(dutiesSeparated), nil
		case c == "payout_recorded_once":
			var first string
			s.db.QueryRow(ctx, `SELECT id::text FROM ledger_entries WHERE payout_reference = $1`, ref).Scan(&first)
			return RecordPayout409JSONResponse{Code: "already_recorded", Message: fmt.Sprintf("transfer %s is already recorded, as entry %s", ref, first)}, nil
		case c == "payout_from_hold_member":
			return RecordPayout422JSONResponse{Code: "not_the_holds_member", Message: dbMessage(err)}, nil
		case c == "withdrawal_names_payout_reference":
			return RecordPayout400JSONResponse{Code: "bad_request", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	m, err := s.movementOf(ctx, entry)
	if err != nil {
		return nil, err
	}
	return RecordPayout201JSONResponse(m), nil
}
