package api

import (
	"context"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// One answer for "no such member" and for "not yours to read": byte-identical,
// so the endpoint cannot be used to discover who is a member where.
var statementNotFound = GetMemberStatement404JSONResponse{Code: "not_found", Message: "no such member"}

// GetMemberStatement serves ONE record in two projections, from ONE query.
// The member sees their statement; an officer of the member's society (any
// current post, the read-only auditor included) sees the same figures plus
// who posted each entry. Everyone else: 404.
func (s *server) GetMemberStatement(ctx context.Context, req GetMemberStatementRequestObject) (GetMemberStatementResponseObject, error) {
	caller := callerOf(ctx)

	var m struct {
		id, number, name, society string
		user                      *string
	}
	if err := s.db.QueryRow(ctx, `SELECT id::text, member_number, full_name, society_id::text, user_id::text
		FROM members WHERE id = $1`, req.MemberId.String()).Scan(&m.id, &m.number, &m.name, &m.society, &m.user); err != nil {
		return statementNotFound, nil
	}

	projection := MemberStatementProjection("")
	if m.user != nil && *m.user == caller {
		projection = Member
	} else {
		var officer bool
		if err := s.db.QueryRow(ctx, `SELECT person_holds_post($1, $2)`, caller, m.society).Scan(&officer); err != nil {
			return nil, err
		}
		if officer {
			projection = Officer
		}
	}
	if projection == "" {
		return statementNotFound, nil
	}

	// The member's own lines only: savings lines naming THIS member. Charges
	// are every charge line of the entry, partner and margin apart (D-03).
	rows, err := s.db.Query(ctx, `
		SELECT e.id::text, e.posted_at, e.value_date::text, e.kind::text, b.reference,
		       e.reverses_id::text, e.adjusts_id::text, e.reason,
		       e.actor_user_id::text, e.actor_post::text, e.actor_post_label,
		       (SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		         WHERE l.entry_id = e.id AND a.role = 'member_savings' AND l.member_id = $1),
		       (SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l WHERE l.entry_id = e.id AND l.charge_role = 'partner_charge'),
		       (SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l WHERE l.entry_id = e.id AND l.charge_role = 'argon_margin')
		  FROM ledger_entries e LEFT JOIN ledger_batches b ON b.id = e.batch_id
		 WHERE e.id IN (SELECT l.entry_id FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		                 WHERE l.member_id = $1 AND a.role = 'member_savings')
		 ORDER BY e.posted_at, e.id`, m.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out                = []StatementRow{} // [] not null, when empty
		opening, balance   int64              // member's side: positive = owed to the member
		chargedP, chargedM int64
	)
	from, to := req.Params.From, req.Params.To
	for rows.Next() {
		var (
			id, kind, actor, label           string
			postedAt                         time.Time
			valueDate, ref, rev, adj, reason *string
			post                             *string
			sav, partner, margin             int64
		)
		if err := rows.Scan(&id, &postedAt, &valueDate, &kind, &ref, &rev, &adj, &reason,
			&actor, &post, &label, &sav, &partner, &margin); err != nil {
			return nil, err
		}
		day := postedAt.Format("2006-01-02")
		if from != nil && day < from.Time.Format("2006-01-02") {
			opening -= sav
			balance -= sav
			continue
		}
		if to != nil && day > to.Time.Format("2006-01-02") {
			continue
		}
		balance -= sav
		chargedP -= partner // a charge is a credit; a refund a debit, which nets it back
		chargedM -= margin

		row := StatementRow{
			EntryId:            uuidOf(id),
			PostedAt:           postedAt,
			Kind:               MovementKind(kind),
			Reference:          ref,
			Direction:          In,
			GrossKobo:          Magnitude(strconv.FormatInt(abs(sav+partner+margin), 10)),
			SavingsLineKobo:    Kobo(strconv.FormatInt(sav, 10)),
			SavingsBalanceKobo: SavingsBalance(strconv.FormatInt(balance, 10)),
			Charges:            []ChargeLine{},
			Reason:             reason,
		}
		if sav > 0 { // a debit to savings: the member's savings fell
			row.Direction = Out
		}
		if valueDate != nil { // NULL stays null: "accumulated before migration", never the posting date
			d, err := time.Parse("2006-01-02", *valueDate)
			if err != nil {
				return nil, err
			}
			row.ValueDate = &openapi_types.Date{Time: d}
		}
		if rev != nil {
			u := uuidOf(*rev)
			row.ReversesEntryId = &u
		}
		if adj != nil {
			u := uuidOf(*adj)
			row.AdjustsEntryId = &u
		}
		if partner != 0 {
			row.Charges = append(row.Charges, ChargeLine{Role: PartnerCharge, AmountKobo: Kobo(strconv.FormatInt(partner, 10))})
		}
		if margin != 0 {
			row.Charges = append(row.Charges, ChargeLine{Role: ArgonMargin, AmountKobo: Kobo(strconv.FormatInt(margin, 10))})
		}
		if projection == Officer {
			pb := struct {
				Post      *StatementRowPostedByPost `json:"post"`
				PostLabel string                    `json:"postLabel"`
				UserId    openapi_types.UUID        `json:"userId"`
			}{PostLabel: label, UserId: uuidOf(actor)}
			if post != nil {
				p := StatementRowPostedByPost(*post)
				pb.Post = &p
			}
			row.PostedBy = &pb
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return GetMemberStatement200JSONResponse(MemberStatement{
		MemberId: uuidOf(m.id), MemberNumber: m.number, MemberName: m.name, SocietyId: uuidOf(m.society),
		Projection: projection, From: from, To: to,
		OpeningSavingsBalanceKobo: SavingsBalance(strconv.FormatInt(opening, 10)),
		ClosingSavingsBalanceKobo: SavingsBalance(strconv.FormatInt(balance, 10)),
		ChargedInPeriod: ChargedInPeriod{
			PartnerChargeKobo: SavingsBalance(strconv.FormatInt(chargedP, 10)),
			ArgonMarginKobo:   SavingsBalance(strconv.FormatInt(chargedM, 10)),
		},
		Rows: out,
	}), nil
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func uuidOf(s string) openapi_types.UUID {
	var u openapi_types.UUID
	_ = u.UnmarshalText([]byte(s))
	return u
}
