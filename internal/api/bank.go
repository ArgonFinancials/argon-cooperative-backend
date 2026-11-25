package api

// A MEMBER IS PAID INTO THEIR OWN BANK ACCOUNT (0040, 2026-11-25).
//
//   PUT /members/{m}/bank-account: the member, on their own step-up
//   (member_bank_account). Recorded with check "checking": the wallet worker,
//   the only holder of VFD's token, asks VFD for the account's name and
//   records it; the database decides whether it resembles the member's name
//   on the register. This server never asks VFD and never records its answer.
//   PUT /societies/{s}/members/{m}/bank-account: an officer, on their own
//   step-up, only for a member who cannot sign in. Recorded with who; that
//   officer never signs the member's withdrawals.
//   POST .../bank-account/confirmation: a current signatory who neither set
//   the details nor is the member confirms a name that differs, with what
//   they looked at.
//   GET /members/{m}/bank-account: the current details and every set, for
//   the member and their society's current officers.
//
// The database checks every rule; each refusal is mapped here.

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	bankAccountShape = regexp.MustCompile(`^[0-9]{10}$`)
	bankCodeShape    = regexp.MustCompile(`^[0-9]{3,6}$`)
)

func (s *server) SetOwnBankAccount(ctx context.Context, req SetOwnBankAccountRequestObject) (SetOwnBankAccountResponseObject, error) {
	member := req.MemberId.String()
	mine, err := s.ownsMember(ctx, member)
	if err != nil {
		return nil, err
	}
	if !mine {
		return SetOwnBankAccount404JSONResponse(notFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return SetOwnBankAccount400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if p, bad := badBankDetails(b.AccountNumber, b.BankCode); bad {
		return SetOwnBankAccount422JSONResponse(p), nil
	}
	out, err := s.setBankAccount(ctx, member, b)
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "bank_account_act":
			return SetOwnBankAccount403JSONResponse(stepUpProblem("member_bank_account")), nil
		case c == "bank_account_number_shape" || c == "bank_code_shape":
			return SetOwnBankAccount422JSONResponse(badAccount), nil
		}
		return nil, err
	}
	return SetOwnBankAccount200JSONResponse(out), nil
}

func (s *server) SetMemberBankAccount(ctx context.Context, req SetMemberBankAccountRequestObject) (SetMemberBankAccountResponseObject, error) {
	society, member := req.SocietyId.String(), req.MemberId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return SetMemberBankAccount404JSONResponse(notFound("member")), nil
	}
	here, err := s.memberHere(ctx, member, society)
	if err != nil {
		return nil, err
	}
	if !here {
		return SetMemberBankAccount404JSONResponse(notFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return SetMemberBankAccount400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if p, bad := badBankDetails(b.AccountNumber, b.BankCode); bad {
		return SetMemberBankAccount422JSONResponse(p), nil
	}
	out, err := s.setBankAccount(ctx, member, b)
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "bank_account_act":
			return SetMemberBankAccount403JSONResponse(stepUpProblem("member_bank_account")), nil
		case c == "bank_account_by_member":
			return SetMemberBankAccount403JSONResponse(Problem{Code: "not_permitted", Message: dbMessage(err)}), nil
		case c == "bank_account_member_can_sign_in":
			return SetMemberBankAccount403JSONResponse(Problem{Code: "member_sets_own", Message: dbMessage(err)}), nil
		case c == "bank_account_number_shape" || c == "bank_code_shape":
			return SetMemberBankAccount422JSONResponse(badAccount), nil
		}
		return nil, err
	}
	return SetMemberBankAccount200JSONResponse(out), nil
}

// setBankAccount: one set, on the caller's act; the database says who and
// whether it was the member.
func (s *server) setBankAccount(ctx context.Context, member string, b *SetBankAccount) (BankAccount, error) {
	var id string
	err := s.onStepUp(ctx, "member_bank_account", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		return x.QueryRow(ctx, `INSERT INTO member_bank_accounts (member_id, account_number, bank_code, set_by_act) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			member, b.AccountNumber, b.BankCode, act).Scan(&id)
	})
	if err != nil {
		return BankAccount{}, err
	}
	rows, err := s.bankAccounts(ctx, `b.id = $1`, id)
	if err != nil {
		return BankAccount{}, err
	}
	if len(rows) != 1 {
		return BankAccount{}, errors.New("api: the bank details just recorded were not found")
	}
	return rows[0], nil
}

func (s *server) ConfirmBankName(ctx context.Context, req ConfirmBankNameRequestObject) (ConfirmBankNameResponseObject, error) {
	society, member := req.SocietyId.String(), req.MemberId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ConfirmBankName404JSONResponse(notFound("member")), nil
	}
	here, err := s.memberHere(ctx, member, society)
	if err != nil {
		return nil, err
	}
	if !here {
		return ConfirmBankName404JSONResponse(notFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return ConfirmBankName400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	// The current details, and whether there is anything for a person to confirm.
	cur, err := s.bankAccounts(ctx, `b.id = (SELECT id FROM member_bank_current WHERE member_id = $1)`, member)
	if err != nil {
		return nil, err
	}
	if len(cur) == 0 {
		return ConfirmBankName404JSONResponse(Problem{Code: "not_found", Message: "this member has no bank details recorded"}), nil
	}
	if cur[0].Check != Differs || cur[0].ConfirmedBy != nil {
		return ConfirmBankName409JSONResponse(nothingToConfirm(cur[0])), nil
	}
	reason := strings.TrimSpace(b.Reason)
	if len(reason) <= 10 {
		return ConfirmBankName422JSONResponse(bankReasonRequired), nil
	}
	id := cur[0].BankAccountId.String()
	err = s.onStepUp(ctx, "bank_name_confirmation", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO bank_name_confirmations (bank_account_id, act_id, reason) VALUES ($1, $2, $3)`, id, act, reason)
		return err
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "bank_name_confirmation_act":
			return ConfirmBankName403JSONResponse(stepUpProblem("bank_name_confirmation")), nil
		case c == "bank_name_confirmed_by_signatory":
			return ConfirmBankName403JSONResponse(Problem{Code: "not_permitted", Message: dbMessage(err)}), nil
		case c == "bank_name_confirmed_by_another":
			return ConfirmBankName403JSONResponse(Problem{Code: "conflict_of_interest", Message: dbMessage(err)}), nil
		case c == "bank_name_confirmation_needed" || c == "bank_name_confirmed_once":
			return ConfirmBankName409JSONResponse(Problem{Code: "nothing_to_confirm", Message: "there is nothing to confirm: it is confirmed already, or the name matches"}), nil
		case c == "bank_name_confirmation_says_why":
			return ConfirmBankName422JSONResponse(bankReasonRequired), nil
		}
		return nil, err
	}
	rows, err := s.bankAccounts(ctx, `b.id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, errors.New("api: the bank details just confirmed were not found")
	}
	return ConfirmBankName201JSONResponse(rows[0]), nil
}

func (s *server) GetBankAccount(ctx context.Context, req GetBankAccountRequestObject) (GetBankAccountResponseObject, error) {
	member := req.MemberId.String()
	// The member, or a current officer of their society: a signatory looks
	// before confirming a name.
	var society string
	var own bool
	err := s.db.QueryRow(ctx, `SELECT society_id::text, coalesce(user_id = $2, false) FROM members WHERE id = $1`, member, callerOf(ctx)).Scan(&society, &own)
	if errors.Is(err, pgx.ErrNoRows) {
		return GetBankAccount404JSONResponse(notFound("member")), nil
	}
	if err != nil {
		return nil, err
	}
	if !own {
		if ok, err := s.officerHere(ctx, society); err != nil || !ok {
			if err != nil {
				return nil, err
			}
			return GetBankAccount404JSONResponse(notFound("member")), nil
		}
	}
	all, err := s.bankAccounts(ctx, `b.member_id = $1`, member)
	if err != nil {
		return nil, err
	}
	out := GetBankAccount200JSONResponse{History: all}
	if len(all) > 0 {
		cur := all[0] // newest first, as member_bank_current orders them
		out.Current = &cur
	}
	return out, nil
}

// bankAccounts: the sets matching where (one parameter), newest first, each
// with VFD's answer and any confirmation.
func (s *server) bankAccounts(ctx context.Context, where, arg string) ([]BankAccount, error) {
	rows, err := s.db.Query(ctx, `SELECT b.id::text, b.account_number, b.bank_code, b.by_member, b.set_by_user::text, su.full_name, b.recorded_at,
		  k.id IS NOT NULL, coalesce(k.found, false), k.vfd_name, k.name_check, c.confirmed_by::text, cu.full_name
		FROM member_bank_accounts b
		JOIN users su ON su.id = b.set_by_user
		LEFT JOIN bank_name_checks k ON k.bank_account_id = b.id
		LEFT JOIN bank_name_confirmations c ON c.bank_account_id = b.id
		LEFT JOIN users cu ON cu.id = c.confirmed_by
		WHERE `+where+` ORDER BY b.recorded_at DESC, b.id DESC`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BankAccount{}
	for rows.Next() {
		var a BankAccount
		var id, setBy, setByName string
		var at time.Time
		var checked, found bool
		var nameCheck, confirmedBy, confirmedName *string
		if err := rows.Scan(&id, &a.AccountNumber, &a.BankCode, &a.ByMember, &setBy, &setByName, &at,
			&checked, &found, &a.VfdName, &nameCheck, &confirmedBy, &confirmedName); err != nil {
			return nil, err
		}
		a.BankAccountId, a.RecordedAt = uuidOf(id), at
		a.SetBy = EmailSetter{UserId: uuidOf(setBy), Name: setByName}
		switch {
		case !checked:
			a.Check = Checking
		case !found:
			a.Check = NotFound
		default:
			a.Check = BankAccountCheck(deref(nameCheck))
		}
		if confirmedBy != nil {
			a.ConfirmedBy = &EmailSetter{UserId: uuidOf(*confirmedBy), Name: deref(confirmedName)}
		}
		a.NameSettled = a.Check == Matches || (a.Check == Differs && a.ConfirmedBy != nil)
		out = append(out, a)
	}
	return out, rows.Err()
}

func badBankDetails(number, code string) (Problem, bool) {
	if !bankAccountShape.MatchString(number) || !bankCodeShape.MatchString(code) {
		return badAccount, true
	}
	return Problem{}, false
}

func nothingToConfirm(a BankAccount) Problem {
	msg := map[BankAccountCheck]string{
		Checking: "the bank has not answered yet: there is no name to confirm",
		NotFound: "the bank says there is no such account: the member sets their details again",
		Matches:  "the name matches the register: there is nothing to confirm",
	}[a.Check]
	if msg == "" {
		msg = "it is confirmed already"
	}
	return Problem{Code: "nothing_to_confirm", Message: msg}
}

var (
	badAccount         = Problem{Code: "bad_account", Message: "an account number is ten digits, and a bank code three to six digits"}
	bankReasonRequired = Problem{Code: "reason_required", Message: "say what you looked at and why the name is the member's, more than ten characters: it is recorded as written"}
)
