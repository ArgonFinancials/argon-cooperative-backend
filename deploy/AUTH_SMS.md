# SMS provider for sign-in codes

The service sends one kind of message: a six-digit code to a member's
registered Nigerian number, on a new-device sign-in, recovery or activation.

## Termii: built, not live (2026-10-29)

The sender is `internal/auth/termii.go`, chosen with `ARGON_SMS=termii` and
`TERMII_URL`, `TERMII_API_KEY`, `TERMII_SENDER_ID` (none defaulted; the URL is
https). It is tested only against a fake Termii server. Until every box below
is ticked, the instance runs `ARGON_SMS=refusing`.

**Go-live list. Each item is checked by a person and dated here.**
- [ ] **(a) One real send, checked against Termii's answer.** The code reads a 2xx with `code: "ok"` and a `message_id`; that shape comes from Termii's documentation and the existing loan platform's code, never from a real answer. Send one code to a staff phone, keep the raw response (the key and number removed), and compare it field by field with the fake in `test/tier1/tier1_sms_termii_test.go`. If they differ, the fake is corrected and the tests rerun before going live.
- [ ] **(b) The sender ID's registration covers this product.** The ID is approved per sender and per use; one registered for another product's alerts may not cover sign-in codes. Confirm with Termii in writing.
- [ ] **(c) Which Termii account sends.** The key alone decides it. It is this product's own account, with its own balance and its own key.
- [ ] **(d) The loan platform's Termii key is rotated** before any account it touches is used here. That key is written in that platform's source (security handover, item 7). Until it is rotated, anyone with that source can send as the account.

**How it behaves (approved 2026-10-28).** One call per code, no retry, the DND route, `+234` numbers only. Four outcomes, each recorded in `sms_sends` (0019) with no text or code:
- *accepted* (a 2xx, a readable body, `code: "ok"`, a message id): `code_sent`.
- *refused* (Termii said no) or *not_reached* (no connection): `code_not_sent`. There is no challenge, and the member is told to try again in a minute, with an officer named only if it keeps failing.
- *unknown* (no answer in time, a 5xx, an unreadable body): `code_sent`, with the challenge kept and word that the code may be late.

Nothing counts against the member's lockout. The code, the key, the full number and the text are never logged (TestSmsTermii08).

**Named gap: delivery receipts.** "Accepted" means Termii took the message, not that the phone got it. Receipts need a signed webhook; that is not built.

## Procurement list (written before a provider was chosen)

Going live needs a real provider, and these items have lead times.

## What a provider must offer
- [ ] **Transactional / DND-bypass route to Nigerian numbers.** Many numbers are on the NCC Do-Not-Disturb list, which blocks promotional SMS. A code sent on a promotional route silently never arrives.
- [ ] **Registered alphanumeric sender ID** (e.g. `ARGON`), approved for Nigerian networks (MTN, Airtel, Glo, 9mobile). Approval takes weeks; start it first.
- [ ] **Message template approval**, where the route requires it, for the code text.
- [ ] **Delivery receipts (DLR)** by webhook, so "code sent" and "code delivered" can be told apart when a member says it never came.
- [ ] **Fallback channel** for weak signal (voice call reading the code, or WhatsApp), per D-21's spirit: a member who can't receive an SMS must still have a path, even if that path is an officer.
- [ ] **API over HTTPS** with per-key IP allowlisting, and keys stored in `/etc/argon`, never in the repo.
- [ ] **Rate limits and per-message pricing** known up front. A re-send every 60 s per member is the ceiling the service enforces.
- [ ] **Data processing agreement** compatible with the Nigeria Data Protection Act 2023. Phone numbers are personal data.
- [ ] **Numbers in E.164** (`+234…`). The register stores `+234…`; the provider must not need local `080…` format.

## Candidates to price (not a recommendation)
Termii, Africa's Talking, Twilio (Nigeria route), Infobip.

## Decided already, and not the provider's to change
- The code never appears in any API response; only the phone receives it.
- 5-minute lifetime, 3 attempts, re-send no sooner than 60 s.
- A code is sent only to the one number all of a person's records agree on. With no such number, there's no code and the person sees an officer.
