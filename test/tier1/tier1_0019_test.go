//go:build tier1

// 0019: EVERY SIGN-IN CODE HANDED TO THE SMS PROVIDER IS RECORDED (the Termii
// sender, piece 3, approved 2026-10-28). One row per send, written once by the
// sign-in service and never changed: which code, which provider, what came of
// it (accepted, refused, not_reached, unknown), and Termii's message id when
// it accepted. "Was this member's code sent?" becomes a lookup; today nobody
// can answer it, on this platform or the loan platform.
//
// What it does NOT say: whether the code reached the phone. "Accepted by
// Termii" and "delivered" are different facts; delivery reports are a later
// stage. And there is NO COLUMN for the message text or the code: a column
// nobody fills is a column someone fills later.
package tier1

import (
	"sort"
	"strings"
	"testing"
)

// smsSend writes one send for a code, as the sign-in service would.
func (a *authT) smsSend(code, outcome string, messageID, providerCode any) error {
	return a.exec(`INSERT INTO sms_sends (code_id, provider, outcome, provider_message_id, provider_code) VALUES ($1, 'termii', $2, $3, $4)`,
		code, outcome, messageID, providerCode)
}

func TestSmsSend01_AnAcceptedSendCarriesItsMessageId(t *testing.T) {
	a := newAuth(t)
	code, _ := a.code(a.who("M"))
	refused(t, a.smsSend(code, "accepted", nil, nil),
		"a send recorded as accepted with no message id: nobody could ever ask Termii about it", w("23514", "sms_accepted_has_message_id"))
}

func TestSmsSend02_OnlyAnAcceptedSendCarriesAMessageId(t *testing.T) {
	a := newAuth(t)
	code, _ := a.code(a.who("M"))
	refused(t, a.smsSend(code, "unknown", "TERMII-"+digest()[:12], nil),
		"a send of unknown outcome recorded with a message id it never got back", w("23514", "sms_accepted_has_message_id"))
}

// Written once and never changed or removed: a record of what happened.
func TestSmsSend03_ASendIsWrittenOnceAndNeverChanged(t *testing.T) {
	a := newAuth(t)
	code, _ := a.code(a.who("M"))
	fixture(t, a.smsSend(code, "accepted", "TERMII-"+digest()[:12], nil), "an accepted send")
	// A change no other rule objects to (another message id, still accepted):
	// only the record's finality can refuse it.
	refused(t, a.exec(`UPDATE sms_sends SET provider_message_id = $2 WHERE code_id = $1`, code, "TERMII-"+digest()[:12]),
		"a send's record changed after the fact", w("23514", "sms_send_is_final"))
	refused(t, a.exec(`DELETE FROM sms_sends WHERE code_id = $1`, code),
		"a send's record removed", w("23514", "sms_send_is_final"))
}

func TestSmsSend04_AMessageIdIsRecordedOnce(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	first, _ := a.code(p)
	id := "TERMII-" + digest()[:12]
	fixture(t, a.smsSend(first, "accepted", id, nil), "the first send")
	a.advance(61e9) // the 60-second resend rule
	second, _ := a.code(p)
	refused(t, a.smsSend(second, "accepted", id, nil),
		"two sends recorded under one Termii message id", w("23505", "sms_message_id_once"))
}

// Termii's refusal code, and only on a refusal: its CODE, never its wording.
func TestSmsSend05_ARefusalCodeOnlyOnARefusal(t *testing.T) {
	a := newAuth(t)
	code, _ := a.code(a.who("M"))
	refused(t, a.smsSend(code, "accepted", "TERMII-"+digest()[:12], "insufficient_balance"),
		"an accepted send carrying a refusal code", w("23514", "sms_provider_code_on_refusal"))
}

// No column for the message text or the code. The columns are exactly these.
func TestSmsSend06_ThereIsNoColumnForTheTextOrTheCode(t *testing.T) {
	rows, err := db.Query(bg(), `SELECT column_name FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'sms_sends'`)
	fixture(t, err, "the columns")
	var got []string
	for rows.Next() {
		var c string
		fixture(t, rows.Scan(&c), "a column")
		got = append(got, c)
	}
	rows.Close()
	sort.Strings(got)
	want := []string{"at", "code_id", "id", "outcome", "provider", "provider_code", "provider_message_id"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("COLUMNS — sms_sends has %v; want exactly %v (no column for the text or the code, so neither can ever be stored)", got, want)
	}
}

// Control: each outcome is recorded as it happened.
func TestSmsSend_Control_EachOutcomeIsRecorded(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	for _, c := range []struct {
		outcome      string
		id, provCode any
	}{
		{"accepted", "TERMII-" + digest()[:12], nil},
		{"refused", nil, "insufficient_balance"},
		{"not_reached", nil, nil},
		{"unknown", nil, nil},
	} {
		code, _ := a.code(p)
		accepted(t, a.smsSend(code, c.outcome, c.id, c.provCode), "a send recorded as "+c.outcome)
		a.advance(61e9)
	}
}
