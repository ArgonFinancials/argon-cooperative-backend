# AWS request: Argon production host

Instance `i-0df0964197555cc26`, us-east-1d, t4g.medium (arm64), Ubuntu 26.04. Region `us-east-1` throughout.
**Part A unblocks deployment by itself. Part B adds backups and the restore drill. Either can be answered first.**

## 0. Confirm before anything
- [ ] Is this the same AWS account as the production loan platform? If yes, every resource below stays separate from it (own role, own SG, own bucket, own key pair), and none is shared.

**Stated requirement: PostgreSQL 18** on the instance, from PGDG (`resolute-pgdg`, checked 2026-10-02: 18.6 for arm64), installed by `deploy/10-install-postgres.sh`, which refuses any other major version or source. This is not something AWS provisions, and nothing on the host forces another version. It is listed here because the database's major version must match local development's (18): an 18 `pg_dump` cannot be restored into 16, so a split would make backups one-way and leave each drill proving only its own version. Outbound 443 to PGDG (A2) is what it needs.

---

## PART A — needed to deploy at all

### A1. SSH key
- [ ] New EC2 key pair `argon-prod-host`, for this instance only. It replaces `prod-loan-wallet`, which is currently the only authorized key.
- [ ] Confirm whether `prod-loan-wallet` opens any other host, in particular the loan platform. If yes, **rotate** it there.

### A2. Security group
- [ ] New SG `argon-prod-host`, replacing `launch-wizard-3` on `i-0df0964197555cc26` (current rules unknown to us)
- [ ] Inbound TCP 22: from `<operator public IP>/32` only. If A3's SSM policy is attached, no inbound rules at all.
- [ ] Inbound TCP 5432: **none**. Postgres binds 127.0.0.1 and is reached only by tunnel.
- [ ] Inbound TCP 8080: **none**. The API sits behind TLS, which is still undecided.
- [ ] Inbound TCP 443: none yet. Added when TLS and DNS are decided.
- [ ] Outbound TCP 443 to `0.0.0.0/0`: PGDG apt, go.dev, snap store, SSM (and S3 for Part B)
- [ ] Outbound TCP 80 to `0.0.0.0/0`: the Ubuntu apt mirror (`us-east-1.ec2.archive.ubuntu.com` is http)
- [ ] Nothing else outbound. The VPC resolver and Amazon Time Sync aren't filtered by security groups.

### A3. Instance role (needed for Part A only if SSM replaces port 22)
- [ ] Role `argon-prod-host`, trust policy `ec2.amazonaws.com` → `sts:AssumeRole`
- [ ] Managed policy `AmazonSSMManagedInstanceCore` (optional: the SSM agent is already running, and this lets port 22 be closed)
- [ ] Instance profile `argon-prod-host`, attached to `i-0df0964197555cc26`. The instance currently has **no** instance profile.

### A4. Operator credentials (read-only, for verifying from outside)
- [ ] Current local credentials fail with `SignatureDoesNotMatch`. Issue new ones, or SSO.
- [ ] IAM user or SSO permission set `argon-operator`, CLI profile `argon`:
  - `ec2:DescribeInstances`, `ec2:DescribeSecurityGroups`, `ec2:DescribeIamInstanceProfileAssociations`
  - `ssm:StartSession` on `i-0df0964197555cc26` only (if A3's SSM policy is used)
  - **no write access to anything**
- [ ] MFA required

### A5. Send back for Part A
- [ ] Key pair name, and the confirmation in A1
- [ ] SG ID and the operator IP in the 22 rule (or "SSM, no 22")
- [ ] Instance-profile ARN (if A3 done)
- [ ] CLI profile name

---

## PART B — needed for backups and the restore drill

### B1. S3 bucket
- [ ] Name `argon-prod-backups-<ACCOUNT_ID>` (must be globally unique; tell us the final name), region `us-east-1`
- [ ] Block Public Access: all four settings **on**. Object Ownership: bucket owner enforced (ACLs disabled).
- [ ] Versioning **enabled**. Default encryption SSE-S3 (`AES256`); the scripts send `--sse AES256`.
- [ ] Bucket policy: deny any request where `aws:SecureTransport` is `false`
- [ ] Object Lock: **your decision.** If wanted, it must be enabled **at creation**: compliance mode, 35 days, on `prod/dumps/`. Nobody, including root, can then delete a recent backup.
- [ ] Lifecycle `prod/dumps/`: expire current versions after 90 days, noncurrent after 30. Lifecycle `prod/drills/`: expire after 365 days.

### B2. Backup permissions on the instance role
- [ ] The A3 role and instance profile, created now if A3 was skipped
- [ ] Inline policy `argon-backup` = `deploy/iam/argon-backup-policy.json`, with `CHANGE-ME-argon-backups` replaced by the B1 bucket name:
  - `s3:ListBucket` on the bucket, condition `s3:prefix` like `prod/*`
  - `s3:PutObject` and `s3:GetObject` on `arn:aws:s3:::<bucket>/prod/*`
  - **no `s3:DeleteObject`, no `s3:PutBucket*`, nothing outside `prod/`**, so a compromised host cannot erase its own backups

### B3. Operator read access to backups
- [ ] Add to `argon-operator`: `s3:ListBucket` on the bucket and `s3:GetObject` on `<bucket>/prod/*`, to confirm backups land. Still no write.

### B4. Send back for Part B
- [ ] Final bucket name
- [ ] Whether Object Lock was enabled
- [ ] Confirmation that `argon-backup` is attached to the instance role

---

## Not needed yet (listed so none of it is a surprise)
- DNS name and TLS certificate (ACM, or Let's Encrypt on the host), when the API is exposed. That's also when inbound 443 opens.
- ~~CloudWatch alarm or SNS topic for a failed backup or drill.~~ Not needed: alerts go to a Slack webhook (`deploy/README.md`, "Alerts"), with an outside heartbeat for a dead server. Nothing to ask AWS for.
- **Not AWS:** an SMS provider for sign-in codes is a separate procurement item.
