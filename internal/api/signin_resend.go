package api

// TOO SOON FOR ANOTHER CODE (ruled 2026-10-30; written against
// TestApiSms07). The PIN was right; another code was asked for inside the
// database's resend minute. Its own answer, never the lockout's: nothing about
// attempts, nothing about pausing, and nothing counted. A page's countdown
// cannot stand in for it (a second tab is a fresh browser).
const resendTooSoonMessage = "Please wait a moment before asking for another code."

func resendTooSoon() PostSignIn429JSONResponse {
	return PostSignIn429JSONResponse{Code: "code_resend_too_soon", Message: resendTooSoonMessage}
}
