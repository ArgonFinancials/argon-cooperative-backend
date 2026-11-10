-- 0029's INTERFACE: "M0" for suite 0029. Money arriving, with NO RULES:
-- notifications and attempts editable, inflows to any account for any amount
-- any number of times, a reconciliation that says nothing, and no exceptions
-- view. Applied on top of the real 0001–0028.
BEGIN;

CREATE TABLE vfd_notifications (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  kind           TEXT NOT NULL,          -- inflow | bvn_consent | unknown
  raw            TEXT NOT NULL,
  signed         BOOLEAN NOT NULL,       -- true: VFD's signature verified; false: VFD does not sign (never trusted alone)
  reference      TEXT,
  account_number TEXT
);

CREATE TABLE vfd_notification_attempts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  notification_id UUID NOT NULL REFERENCES vfd_notifications(id),
  at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  result          TEXT NOT NULL,         -- confirmed | duplicate | not_confirmed | unmatched | malformed
  detail          TEXT
);

CREATE TABLE vfd_inflows (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vfd_reference      TEXT NOT NULL,
  society_id         UUID NOT NULL REFERENCES societies(id),
  account_number     TEXT NOT NULL,
  amount_kobo        BIGINT NOT NULL,
  session_id         TEXT,
  originator_name    TEXT,
  originator_account TEXT,
  originator_bank    TEXT,
  narration          TEXT,
  vfd_time           TEXT,
  notification_id    UUID REFERENCES vfd_notifications(id),
  confirmed_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE vfd_reconciliation_runs (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_number TEXT NOT NULL,
  period_from    TIMESTAMPTZ NOT NULL,
  period_to      TIMESTAMPTZ NOT NULL,
  ran_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  outcome        TEXT NOT NULL,         -- could_not_run | matched | missing_found
  detail         TEXT
);

-- The exceptions view's SHAPE, showing nothing: what it shows is 0029's rule.
CREATE VIEW wallet_exceptions AS
  SELECT ''::text AS kind, NULL::uuid AS society_id, NULL::uuid AS subject_id, NULL::timestamptz AS since, ''::text AS detail WHERE false;

COMMIT;
