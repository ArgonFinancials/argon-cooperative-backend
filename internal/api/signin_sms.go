package api

// WHAT A MEMBER IS TOLD WHEN A CODE IS NOT SENT, OR MAY BE LATE (the Termii
// sender, piece 2, approved 2026-10-28; written against
// test/tier1/tier1_api_sms_test.go). For the member, to show as written:
// nothing about providers or what failed, which is for us (the sender's log
// line and sms_sends).
//
//   - code_not_sent (Termii refused, or could not be reached): no challenge,
//     since no code is on its way. "A minute" is the database's own resend
//     rule; the member is not sent to an officer by our outage, and the
//     officer is named only as the fallback if it keeps failing.
//   - code_sent with a late code (no readable answer from Termii): the
//     challenge is kept, because the code may arrive and must work if it does.
const (
	codeNotSentMessage = "We couldn't send your code just now. Please try again in a minute. If it keeps failing, an officer of your society can confirm this phone with you in person."
	codeDelayedMessage = "Your code may take a little longer to arrive. If it hasn't come in a minute, try again."
)

func codeNotSent() PostSignIn200JSONResponse {
	msg := codeNotSentMessage
	return PostSignIn200JSONResponse{Outcome: CodeNotSent, Message: &msg}
}

func codeSent(challenge string, delayed bool) PostSignIn200JSONResponse {
	out := PostSignIn200JSONResponse{Outcome: CodeSent, Challenge: &challenge}
	if delayed {
		msg := codeDelayedMessage
		out.Message = &msg
	}
	return out
}
