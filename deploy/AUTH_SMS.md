# SMS provider for sign-in codes: procurement list

The service sends one kind of message: a six-digit code to a member's
registered Nigerian number, on a new-device sign-in, recovery or activation.
Tests use a fake (`auth.SMS`). Going live needs a real provider, and these
items have lead times.

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
