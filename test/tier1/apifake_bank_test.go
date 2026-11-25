//go:build tier1

// The reference for a member's bank details over HTTP (0040), breakable by
// name (ARGON_TEST_API_FAKE=<break>), for tier1_api_bank_test.go:
//
//	bank_own_anyone            any member may set another's: the database refuses (500)
//	bank_shape_db              a malformed number reaches the database (500)
//	bank_officer_unmapped      an officer setting a member who signs in: the database refuses (500)
//	bank_said_matches          details VFD has not answered on shown as matching
//	bank_settled_unconfirmed   a name that differs shown as settled before anyone confirms it
//	bank_history_oldest        the oldest set shown as current
//	bank_get_anyone            any signed-in person reads a member's bank details
//	bank_confirm_any           a confirmation tried on a name that matches: the database refuses (500)
//	bank_confirm_setter_db     the setter confirming: the database refuses (500)
//	bank_reason_db             a confirmation with no reason: the database refuses (500)
//	bank_hold_unmapped         the hold's refusal over bank details answered 500, not the member's sentence
//	bank_request_unmapped      a request with no bank details answered 500
//	bank_setter_signs_unmapped the setter's signature refused as 500
package tier1

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/api"
)

var (
	fakeBankNumber = regexp.MustCompile(`^[0-9]{10}$`)
	fakeBankCode   = regexp.MustCompile(`^[0-9]{3,6}$`)
	// fakeBankRefusals: the hold's refusals (0040) and their codes.
	fakeBankRefusals = map[string]string{"payout_needs_bank_account": "needs_bank_account", "payout_bank_not_checked": "bank_not_checked",
		"payout_bank_not_found": "bank_not_found", "payout_bank_name_unconfirmed": "bank_name_unconfirmed"}
)

func (f *fakeAPI) SetOwnBankAccount(ctx context.Context, req api.SetOwnBankAccountRequestObject) (api.SetOwnBankAccountResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	member := req.MemberId.String()
	var mine bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND user_id = $2)`, member, caller).Scan(&mine)
	if !mine && f.brk != "bank_own_anyone" {
		return api.SetOwnBankAccount404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	b := req.Body
	if (!fakeBankNumber.MatchString(b.AccountNumber) || !fakeBankCode.MatchString(b.BankCode)) && f.brk != "bank_shape_db" {
		return api.SetOwnBankAccount422JSONResponse{Code: "bad_account", Message: "ten digits; three to six"}, nil
	}
	out, err := f.bankSet(ctx, caller, member, b)
	if err != nil {
		if c := pgConstraint(err); lockStepUpRefused[c] || c == "bank_account_act" {
			return api.SetOwnBankAccount403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for member_bank_account"}, nil
		}
		return nil, err
	}
	return api.SetOwnBankAccount200JSONResponse(out), nil
}

func (f *fakeAPI) SetMemberBankAccount(ctx context.Context, req api.SetMemberBankAccountRequestObject) (api.SetMemberBankAccountResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society, member := req.SocietyId.String(), req.MemberId.String()
	var here bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2)`, member, society).Scan(&here)
	if !f.officerHere(ctx, caller, society) || !here {
		return api.SetMemberBankAccount404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	b := req.Body
	if !fakeBankNumber.MatchString(b.AccountNumber) || !fakeBankCode.MatchString(b.BankCode) {
		return api.SetMemberBankAccount422JSONResponse{Code: "bad_account", Message: "ten digits; three to six"}, nil
	}
	out, err := f.bankSet(ctx, caller, member, b)
	if err != nil {
		switch c := pgConstraint(err); {
		case lockStepUpRefused[c] || c == "bank_account_act":
			return api.SetMemberBankAccount403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for member_bank_account"}, nil
		case c == "bank_account_by_member":
			return api.SetMemberBankAccount403JSONResponse{Code: "not_permitted", Message: dbMessage(err)}, nil
		case c == "bank_account_member_can_sign_in" && f.brk != "bank_officer_unmapped":
			return api.SetMemberBankAccount403JSONResponse{Code: "member_sets_own", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	return api.SetMemberBankAccount200JSONResponse(out), nil
}

func (f *fakeAPI) bankSet(ctx context.Context, caller, member string, b *api.SetBankAccount) (api.BankAccount, error) {
	var id string
	err := pgx.BeginFunc(ctx, f.db, func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('member_bank_account', $1, $2) RETURNING id`, caller, b.PinProofId.String()).Scan(&act); err != nil {
			return err
		}
		return x.QueryRow(ctx, `INSERT INTO member_bank_accounts (member_id, account_number, bank_code, set_by_act) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			member, b.AccountNumber, b.BankCode, act).Scan(&id)
	})
	if err != nil {
		return api.BankAccount{}, err
	}
	rows, err := f.bankRows(ctx, `b.id = $1`, id)
	if err != nil || len(rows) == 0 {
		return api.BankAccount{}, err
	}
	return rows[0], nil
}

func (f *fakeAPI) ConfirmBankName(ctx context.Context, req api.ConfirmBankNameRequestObject) (api.ConfirmBankNameResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society, member := req.SocietyId.String(), req.MemberId.String()
	var here bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2)`, member, society).Scan(&here)
	if !f.officerHere(ctx, caller, society) || !here {
		return api.ConfirmBankName404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	cur, err := f.bankRows(ctx, `b.id = (SELECT id FROM member_bank_current WHERE member_id = $1)`, member)
	if err != nil {
		return nil, err
	}
	if len(cur) == 0 {
		return api.ConfirmBankName404JSONResponse{Code: "not_found", Message: "no bank details"}, nil
	}
	if (cur[0].Check != api.Differs || cur[0].ConfirmedBy != nil) && f.brk != "bank_confirm_any" {
		return api.ConfirmBankName409JSONResponse{Code: "nothing_to_confirm", Message: "nothing to confirm"}, nil
	}
	reason := strings.TrimSpace(req.Body.Reason)
	if len(reason) <= 10 && f.brk != "bank_reason_db" {
		return api.ConfirmBankName422JSONResponse{Code: "reason_required", Message: "say what you looked at"}, nil
	}
	id := cur[0].BankAccountId.String()
	err = pgx.BeginFunc(ctx, f.db, func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('bank_name_confirmation', $1, $2) RETURNING id`, caller, req.Body.PinProofId.String()).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(ctx, `INSERT INTO bank_name_confirmations (bank_account_id, act_id, reason) VALUES ($1, $2, $3)`, id, act, reason)
		return err
	})
	if err != nil {
		switch c := pgConstraint(err); {
		case lockStepUpRefused[c] || c == "bank_name_confirmation_act":
			return api.ConfirmBankName403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for bank_name_confirmation"}, nil
		case c == "bank_name_confirmed_by_signatory":
			return api.ConfirmBankName403JSONResponse{Code: "not_permitted", Message: dbMessage(err)}, nil
		case c == "bank_name_confirmed_by_another" && f.brk != "bank_confirm_setter_db":
			return api.ConfirmBankName403JSONResponse{Code: "conflict_of_interest", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	rows, err := f.bankRows(ctx, `b.id = $1`, id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return api.ConfirmBankName201JSONResponse(rows[0]), nil
}

func (f *fakeAPI) GetBankAccount(ctx context.Context, req api.GetBankAccountRequestObject) (api.GetBankAccountResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	member := req.MemberId.String()
	var society string
	var own bool
	f.db.QueryRow(ctx, `SELECT society_id::text, coalesce(user_id = $2, false) FROM members WHERE id = $1`, member, caller).Scan(&society, &own)
	if society == "" || !own && !f.officerHere(ctx, caller, society) && f.brk != "bank_get_anyone" {
		return api.GetBankAccount404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	all, err := f.bankRows(ctx, `b.member_id = $1`, member)
	if err != nil {
		return nil, err
	}
	out := api.GetBankAccount200JSONResponse{History: all}
	if len(all) > 0 {
		cur := all[0]
		if f.brk == "bank_history_oldest" {
			cur = all[len(all)-1]
		}
		out.Current = &cur
	}
	return out, nil
}

func (f *fakeAPI) bankRows(ctx context.Context, where, arg string) ([]api.BankAccount, error) {
	rows, err := f.db.Query(ctx, `SELECT b.id::text, b.account_number, b.bank_code, b.by_member, b.set_by_user::text, su.full_name, b.recorded_at,
		  k.id IS NOT NULL, coalesce(k.found, false), k.vfd_name, k.name_check, c.confirmed_by::text, cu.full_name
		FROM member_bank_accounts b JOIN users su ON su.id = b.set_by_user
		LEFT JOIN bank_name_checks k ON k.bank_account_id = b.id
		LEFT JOIN bank_name_confirmations c ON c.bank_account_id = b.id
		LEFT JOIN users cu ON cu.id = c.confirmed_by
		WHERE `+where+` ORDER BY b.recorded_at DESC, b.id DESC`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.BankAccount{}
	for rows.Next() {
		var a api.BankAccount
		var id, setBy, setByName string
		var at time.Time
		var checked, found bool
		var check, confirmedBy, confirmedName *string
		if err := rows.Scan(&id, &a.AccountNumber, &a.BankCode, &a.ByMember, &setBy, &setByName, &at, &checked, &found, &a.VfdName, &check, &confirmedBy, &confirmedName); err != nil {
			return nil, err
		}
		a.BankAccountId, a.RecordedAt = uuidOf(id), at
		a.SetBy = api.EmailSetter{UserId: uuidOf(setBy), Name: setByName}
		switch {
		case !checked && f.brk == "bank_said_matches":
			a.Check = api.Matches
		case !checked:
			a.Check = api.Checking
		case !found:
			a.Check = api.NotFound
		default:
			a.Check = api.BankAccountCheck(*check)
		}
		if confirmedBy != nil {
			n := ""
			if confirmedName != nil {
				n = *confirmedName
			}
			a.ConfirmedBy = &api.EmailSetter{UserId: uuidOf(*confirmedBy), Name: n}
		}
		a.NameSettled = a.Check == api.Matches || a.Check == api.Differs && (a.ConfirmedBy != nil || f.brk == "bank_settled_unconfirmed")
		out = append(out, a)
	}
	return out, rows.Err()
}
