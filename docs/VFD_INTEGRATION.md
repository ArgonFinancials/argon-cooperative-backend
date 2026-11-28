# VFD integration: what we know, and how we build it

**Status:** spec (2026-10-13); **built in stage 3 (2026-11-10)**, below. Decided: re-implement the VFD boundary in Go, in this repository (LEDGER_CONTRACT.md §11).

**Built (stage 3, 2026-11-10), each requirement of §2 to its code and its proof:**

| # | Requirement | Where | Proof |
|---|---|---|---|
| 1 | A timeout on every call, every status checked | `internal/vfd` (15 s default; four outcomes; requery states) | suite vfd: 20 tests, 26 breaks |
| 2 | Signed webhooks, no unsigned path | `POST /webhooks/vfd`; `ARGON_VFD_WEBHOOK` is hmac, unsigned or off, no default | suite webhook_api |
| 3 | De-duplicate by VFD's reference, under a unique constraint | `vfd_inflows` (`vfd_inflow_once`) | suites 0029, wallet |
| 4 | Record raw, then process, then acknowledge | `vfd_notifications`, kept before the 200; processing in the worker | suites webhook_api, 0029 |
| 5 | Reprocess failures; unmatched is an item for a person | every attempt recorded; `wallet_exceptions` | suites 0029, wallet |
| 6 | Pull statements for missed notifications | the worker asks daily; **no statement endpoint is known**, so each run is recorded as could_not_run and shown as an exception | suites 0029, wallet |
| 7 | Refund only on a definitive failure, idempotently | 0028: failed only on a refused or unreached send, or "not found" 2 hours after it (PROVISIONAL); the release is the outcome's own, once | suites 0028, wallet |
| 8 | Never invent an account number | `internal/vfd` (ten digits as a string, or no account; no fallback endpoint); `vfd_accounts` (shape, once, client id) | suites vfd, 0028 |
| 9 | Deterministic references | `vfd_transfer_reference(hold)`, enforced by 0028 | suite 0028 |
| 10 | Hold before paying; record the outcome | the last signature places the hold (§13); the system posts what VFD paid, flagged if different | suites 0028, approvals_api, wallet |
| 11 | Money in is credited only on VFD's word, to the member it is certainly from | 0032: a member's claim (session id and exact amount, no other claimant) or the member's own VFD account; 0033: anything else by two officers, at VFD's amount; `contribution_rematch` on the tick for a race | suites 0032, 0033, claims_api, resolutions_api, wallet |
| 12 | Credentials from the environment only | `ARGON_VFD`, `VFD_BASE_URL` (https), `VFD_ACCESS_TOKEN`; the token, a BVN and a birth date in no log line | suite vfd |

**Built again, 2026-11-24, from production evidence** (the wallet platform's "Connecting to VFD: what the wallet platform has proven in production", 24 November 2026: all 193 of its outward transfers looked up at VFD one by one, each checked against what really happened). The reviewer: build its §1.6 exactly as written, the deposit sweeper from day one, every send and requery reply stored. What changed (0039, `internal/vfd`, `internal/wallet`):

| What production showed | What is built |
|---|---|
| 161 of 161 sends answered `00` were paid | **VFD's `00` to the send is the payment**, posted at once for the transfer's amount |
| A refused send is not proof nothing happened: in 3 of 32, VFD had created the transfer and sent it | **Anything other than `00`, a timeout included, is a pending check.** The money stays held. A refused send no longer fails a transfer |
| `108` "No Transaction!" was right 29 times of 29 | **Released only on `108`, twice in a row, both 30 minutes or more after the send** (`vfd_settlement_window()` is now 30 minutes). Only status `108`: "no transaction" under another code holds |
| The one real `transactionStatus 99` was money that never arrived; `91` and `25` were sent out | **Paid only on `00` with `transactionStatus 00`.** Anything else (91, 25, 99, a word) **holds, and a person is told**: `wallet_exceptions`' `transfer_needs_vfd`, an error in the worker's log, and the operator's alert (`66-check-vfd-holds.sh`, on the freshness timer). Never released by the system |
| The wallet re-queried erratically, or never | **Re-queried at about 2 minutes, every 10 minutes for 24 hours, then hourly** |
| The wallet stored no failed reply, so nobody can say which send codes led where | **Every send reply and every requery reply kept**, HTTP status and body as received (`vfd_transfer_events.http_status`, `reply`) |
| All money sits in one pool; sub-accounts show ₦0 (deposits swept) | **Every transfer leaves from the pool**, the account VFD's `GET /account/enquiry` names, recorded once (`vfd_pool`) |
| Notifications stopped for days, four times, and were never re-sent | **The sweeper**: each society account's history (`GET /account/transactions`, page by page, in WAT, every 15 minutes) is read; every `INFLOW` no notification brought is kept as a notification from the history (`source = 'history'`) and confirmed by the same requery as any other |
| VFD never signs notifications (0 of 55) | Unchanged: `unsigned` is the mode to run; a notification is never believed (§3.2: requeried, amount and account compared unconditionally, `transactionType INFLOW` required) |

**Built again, 2026-11-25 (0040): a payout goes into the member's own bank account**, inter-bank, not their VFD sub-account (which §1.1 shows is swept into the pool: the member would be shown paid and the money never leave). The member sets the account number and bank on their own step-up (an officer only for a member with no PIN). The wallet worker runs VFD's name enquiry (`GET /transfer/recipient`, `transfer_type=inter`) on each new set and records VFD's answer (`bank_name_checks`). The database judges the name against the register, and a signatory confirms one that differs. The worker asks again before every send, and a name that is not the one recorded waits. No bank details: refused when the member asks for a withdrawal, and at the last signature. LEDGER_CONTRACT.md §12, 2026-11-25.

**The same credentials as the wallet (2026-11-25).** The cooperative sends from the wallet platform's pool on its token. Its references are therefore its own, "COOP-" and the hold's 32 hex characters, never the wallet's "Argon-" (0041). No member's bank details are taken at VFD's own bank code (090110): a payout to an account on the same credentials would never leave the pool. VFD's notifications go to the wallet's address, so the webhook stays off and the sweeper finds deposits. The one thing left to learn from VFD is whether it restricts by IP (deploy/README.md, the wallet: the check from the instance).

**Still for VFD** (the document's §9): every `transactionStatus` code and which are final; whether VFD restricts by IP; whether the cooperative can have its own credentials; whether notifications will be signed or retried.

**Provenance:** a read-only review of Argon's running wallet platform (`wallet-service-backend`), which has integrated VFD in production since 2025. Nothing there was changed or run. Its credentials were not opened. What follows is the knowledge it holds, and the defects it found the hard way, written as requirements.

**What this is not:**
- It is not a decision about where a society's money is held. Pooled or segregated is open (§6), and nothing here may assume either.
- It is not VFD's documentation. Anything marked **UNVERIFIED** must be confirmed against VFD's own documentation, or with whoever holds the VFD relationship, before we rely on it.

---

## 1. What the running platform shows about VFD

### Authentication
- Every request carries a static access token in an `AccessToken` header, from configuration.
- There is no token refresh in production code; rotation is manual.
- An OAuth client-credentials exchange exists only in a diagnostic tool. **UNVERIFIED** whether VFD expects it for production.
- **The cooperative's route (2026-11-28).** VFD accepts only the wallet server's address. The cooperative's calls go through the relay on that server (`cmd/vfd-proxy`, `deploy/vfd-proxy/README.md`). The relay adds the token there, and the cooperative never holds it. Calls are signed with HMAC-SHA256 (`internal/vfdsign`). An answer marked `X-Argon-Proxy` is the relay's own, never VFD's: the client reads it as not reached, or as unknown for `vfd_timeout`.

### Endpoints in use

| Purpose | Method and path |
|---|---|
| BVN consent, KYC lookup | `GET /bvn-consent?type=02&bvn=` |
| Lift post-no-debit after consent | `POST /client/release` |
| Open an individual account | `POST /client/tier/individual`, falling back to `POST /client/create?bvn=&dateOfBirth=` |
| Open another account for an existing client (after code 929) | `POST /client/individual` `{previousAccountNo}` |
| Bank list | `GET /bank` |
| Name enquiry | `GET /transfer/recipient?accountNo=&bank=&transfer_type=intra or inter` |
| The pool account (source of payouts) | `GET /account/enquiry` |
| Transfer, intra-bank or inter-bank | `POST /transfer` |
| A transfer's outcome, by reference | `GET /transactions?reference=` |
| Collection (virtual) accounts | `POST /virtualaccount` |
| Simulated deposit | `POST /credit` (**sandbox only by its own description; never use it to move money**) |

### Opening an account
- A **BVN is mandatory**; NIN is not used.
- Names must be sent with surname as `lastname` and other names as `firstname`. The running platform sent them swapped for months.
- **Code 929 means "client account exists":** VFD then expects `/client/individual` with the existing account number, and that call opens *another* account.
- If no account number comes back, the next step is `/bvn-consent`. If that returns a URL, the customer must consent before an account can be opened.
- Inter-bank or intra-bank routing is by bank code. VFD's own code (`090110`) means intra-bank. Sending a VFD-to-VFD payout as inter-bank failed silently while still debiting.

### Webhooks
- Two notifications: inflow credit, and BVN consent.
- Inflow fields in use: `reference`, `amount` (seen as both a string and a number), `account_number` (also seen as `accountNo` / `accountNumber`), `originator_*`, `timestamp`, `session_id`, `transaction_channel`.
- **UNVERIFIED:** whether VFD signs inflow notifications, how it retries, and on what schedule. The running platform's comment says standard inflows are not signed.

### Transfers
- The transfer is signed with `hex(SHA512(fromAccount + toAccount))`.
- Success on submission is status `"00"`.
- Outcome by requery: `/transactions?reference=`. The running platform treats status `00` with `transactionStatus` in {`00`, `99`, `SUCCESS`, `SUCCESSFUL`} as success. **`99` as success is UNVERIFIED.**
- "Not found" appears as status `108` or a message containing "no transaction".

### Charges
- **VFD's own charge to Argon appears in no response, webhook or record** of the running platform. It is unknown (LEDGER_CONTRACT §6: whoever holds the VFD relationship).
- The fee that platform plans to charge members is Argon's own income (§9 corrected 2026-10-13), on an unmerged branch.

---

## 2. Requirements: the knowledge, none of the defects

Each requirement names the defect it prevents.

1. **A timeout on every VFD call, and every status checked.** No HTTP client without a timeout. A non-2xx response is an error. A response is parsed strictly, and a parse failure is an error.
   *Defect:* the running client had no timeout and returned success on any HTTP response, so a VFD refusal credited the receiver.

2. **Signed webhooks, with no unsigned path.** Every inflow is verified with a dedicated webhook secret. A request without a valid signature is refused. If VFD does not sign a notification type, that type is never trusted on its own: it is confirmed against VFD (a requery or a statement) before anything is credited.
   *Defect:* the signature was checked only if the header was present, so anyone could credit any wallet. A dedicated secret was added and later lost in a sync commit.

3. **De-duplicate by VFD's reference, under a unique constraint.** The provider's reference is the identity of an inflow, not a hash of the body.
   *Defect:* body-hash de-duplication let any retry with a changed byte through; de-duplication by reference was added and then reverted.

4. **Record the raw webhook durably, in one transaction, then process, then acknowledge.**
   - Store the raw notification first.
   - Process it in one transaction (the inflow, its ledger entry, its lines).
   - Answer VFD only after the record is durable.
   - A failure part-way leaves a recorded, unprocessed notification. It never leaves a half-posted credit.
   *Defect:* four separate writes, no transaction, always answering 200, so failures were silently dropped and VFD never retried.

5. **Reprocess failures.** Unprocessed notifications are picked up and retried by a job, and each attempt is recorded. A notification that cannot be processed (no matching account, a malformed payload) becomes an item for a person, never a log line.
   *Defect:* a processed flag nothing ever read; inflows to unmatched accounts dropped.

6. **Pull statements to find missed notifications.** On a schedule, fetch VFD's statement (or transaction list) per account and compare it with recorded inflows. A missing inflow becomes an item to resolve. This is part of the reconciliation obligation (§11).
   *Defect:* no polling at all; missed deposits found by hand months later.

7. **Refund only on a definitive failure, and idempotently.**
   - A payout's outcome is one of: confirmed success, **confirmed** failure, or unknown.
   - Unknown stays unknown, and is requeried and escalated. It is never refunded.
   - A refund carries a deterministic reference, so it happens at most once.
   - "Not found" moments after submission is not a definitive failure (settlement lag). Only VFD's explicit failure status, or not-found after a defined settlement window, counts. **UNVERIFIED:** VFD's statuses and timings.
   *Defect:* immediate refund on "not found", then a late payout, so the customer was paid twice; ambiguous outcomes left customers debited with no refund.

8. **Never invent an account number.** If VFD does not issue a number, there is no account, and the record says so. Store VFD's client id alongside the account number. The account number has a unique constraint.
   *Defect:* a fallback that saved `"10"` plus eight random digits as an active account; placeholder numbers in production.

9. **Deterministic references for everything sent to VFD,** derived from the authorisation that caused it, so a retry can never become a second payment.
   *Defect:* time-based and random references; a task retried 16 times posted 16 times.

10. **Hold before paying; always record the outcome** (LEDGER_CONTRACT §11). The ledger checks and holds the amount when a payout is authorised to execute. It then records whatever VFD confirms, flagging any difference for a person.
    *Defect, here as well as there:* checking at recording time refuses to record money that has already left.

11. **No boot-time data changes, and no repair tool that sets balances.** A correction is a new entry, attributed and reasoned.
    *Defect:* seeders that zeroed balances and forced account numbers on every deploy; repair tools that overwrote balances and deleted rows.

12. **Credentials only from the environment, and a service refuses to start without them.** No defaults that work, and no printing of any part of a credential.
    *Defect:* hard-coded database passwords and API keys; checks that switched off when a secret was empty; a tool that printed a token prefix.
