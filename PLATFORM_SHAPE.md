# The platform role: the shape (DRAFT for review, 2026-10-07)

**Status: a proposal.** No schema, no tests, no endpoint. When it is approved it
becomes the contract the platform tests are written against. Sources:
- D-14 (open access, logged, visible to the society);
- D-15 (no administrator resets a signatory);
- the frontend's platform console: `lib/platformOps.ts` (`STAFF_ROLES`, `STAFF_CREDENTIALS`, `OPEN_QUESTIONS`, offboarding).

Today the backend has **no platform role at all**. Everyone is a person with
member records and appointments in societies. Argon staff fit nowhere.

## 1. The boundary, first

**Argon staff run the platform. They never act as a society.**
- They see everything (D-14).
- They configure and support.
- They **never** move a society's money, sign or reject a release, approve a
  loan, change a signatory, or reset anyone's credentials.

This is the frontend's open question q1 ("assumed yes"). It has to be decided
rather than inherited. If a recovery case ever needs staff to move funds, that is
a different product under a different licence.

The boundary is enforced **by the database**, the same way the API role is held
to what it may do. It is not enforced by the console hiding buttons.

## 2. Who staff are

- **A staff record of its own**, not an appointment. `platform_staff`: the person
  (user), the role (`support`, `engineering`, `finance`), and the dates it was held
  from and until. It is point-in-time, like posts: a past action reads as taken
  by the role held then.
- **Staff never hold a signing post, anywhere, while they are staff.** This is
  database-enforced in both directions, like the auditor rule. A staff member who
  signs releases in their own cooperative would hold both sides of the boundary.
  **Decision:** may staff be ordinary *members* of a society? I propose yes, with
  no post. Their own member record is then read like anyone else's, and logged.

## 3. How staff sign in: a separate surface, a stronger factor

The frontend's `STAFF_CREDENTIALS` holds the platform to a stronger standard than
societies:
- a **hardware security key** at every sign-in, where an SMS code is not accepted;
- **20-minute** sessions that end when idle;
- named accounts only;
- access re-attested every quarter.

The backend has PIN plus SMS code and nothing else. So, as a proposal:
- **A platform session is its own kind,** issued only after a hardware-key
  (WebAuthn) assertion. The step-up for any intervention is a fresh key
  assertion, not a PIN.
- **Platform sessions reach only `/platform/*`,** and society sessions never
  reach it.
- **There is no platform surface until WebAuthn exists.** Building the console
  on PIN and SMS would be building it on the factor the design rejects.

## 4. Reads: open, and every one recorded (D-14)

**"The record, not a gate"** (the frontend's q2 and q3). Staff open any society's
record directly, with no request step. **Every read is written down, and the
society sees it in its own console in the same words.**

To make "every read" true rather than intended:
- **A separate database role, `argon_platform`, with NO SELECT on any society
  table.** It reads only through functions that write the access record first and
  return the data second, in one transaction.
- **If the record cannot be written, the read fails.** There is no path to an
  unrecorded view.
- **The access record:** who (staff, role at the time), what (society, member,
  record, endpoint), when. It is **append-only, and deletable by nobody, Argon
  included.** Retention is assumed at seven years (q5, open with counsel).
- **The society reads its own access record** through the books' officer rule:
  current officers, the auditor included.
- **A society that leaves takes the record with it** (offboarding step 2, PRD 8.7).

## 5. Interventions: support actions, never society actions, never alone

**Support and engineering may:**
- re-trigger a webhook;
- unstick a sync;
- correct an account or member mapping;
- escalate to VFD;
- **raise** a reversal request, which the society's signatories then decide.
  Staff never sign it.

**Every intervention:**
- needs a **second named staff member's approval** (`STAFF_CREDENTIALS` c4,
  "the same rule the product sells to societies");
- takes the hardware-key step-up;
- is recorded with the society able to see it, like a read;
- **never** writes a ledger line, a signature, a rejection, an appointment or a
  credential act. The database refuses all of these for the platform role.

## 6. Configuration: Finance, versioned, never edited

- **Who may change:** Finance only, and every change needs a second staff
  approval.
- **What it covers:** charge rules and rates (four of them still open with the
  Board, q4), underwriters, tiers, and platform bounds on society settings.
- **How a change happens:** a configuration change is a new row with an
  "effective from" date, never an edit. A charge taken in March is computed by
  March's rule after June's change. This is the officer-post rule again,
  applied to configuration.
- **What Finance doesn't touch:** a society's own settings (guard threshold,
  signatures, the write-off quorum) belong to the society. Finance sets only the
  **bounds** they must fall within, not the values.

## 7. A society's lifecycle

The enum exists today: prospect → verifying → provisioning → handover → active →
dormant → departed.
- **Staff move a society along it** up to `handover`.
- **The handover gives the society's officers control,** and staff authority
  over that society ends at the boundary in §1.
- **A society leaves** only by its own minuted resolution (offboarding step 1).
  Funds go out by a closing release that the **society's** signatories authorise.
  Staff access ends that day, and the access record is kept.

## 8. Decisions this needs

| Decision | Owner |
|---|---|
| q1: platform authority stops at configuration and support; no staff fund movement, ever | Product + Legal |
| May staff be members of a society (with no post)? Proposed yes | Product |
| A hardware key as the platform's only factor, so no platform surface before WebAuthn | Product + Security |
| Does a staff read state a purpose? (D-14 says no request step; a free-text purpose would not delay a read but would make the record more useful) | Product + Legal |
| Retention and deletion of the access record (q5: seven years, deletable by nobody) | Legal |
| The charge rules themselves (q4: four counts) | Board |
| Is a second staff approval needed for reads of individual member records too, or only interventions? D-14 says reads are open; proposed: interventions only | Product |

## 9. What it would touch in the existing schema

- **New:** `platform_staff`, the access record, interventions with their second
  approval, versioned configuration.
- **New database role:** `argon_platform`, with nothing granted on society tables.
- **A rule on `appointments`:** no signing post while staff.
- **`credential_acts`:** a platform user is never an actor. The D-15 refusal
  extends to staff.
- **Unchanged:** every society rule built so far. The platform sits outside it.
