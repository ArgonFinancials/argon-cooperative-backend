//go:build tier1

// ACCEPTANCE TESTS — 0021: A DECIDED REQUEST TAKES NO MORE SIGNATURES (ruled
// 2026-10-30). Once a request has its quorum it is decided: a further
// signature is a signature on nothing, recording a person as authorising what
// was already authorised, and the count of signatures would no longer be the
// count that approved it.
package tier1

import "testing"

func TestDecided01_ASignatureOnAnApprovedRequestIsRefused(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.s.sign(req, w.tMem, "treasurer"), "first signature")
	fixture(t, w.s.sign(req, w.cMem, "chairman"), "second signature")
	refused(t, w.s.sign(req, w.sMem, "secretary"), "a third signature on a request two have approved", refusedAs("signature_on_decided_request"))
}

// The signature that REACHES the quorum lands: the rule refuses what comes
// after a decision, never the deciding signature itself.
func TestDecided02_Control_TheDecidingSignatureLands(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.s.sign(req, w.tMem, "treasurer"), "first signature")
	accepted(t, w.s.sign(req, w.cMem, "chairman"), "the second of two: the signature that decides")
	wantStatus(t, req, "", "approved", "two of two")
}
