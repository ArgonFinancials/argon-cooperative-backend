# AWS request: Argon production host

Account: the one holding instance `i-0df0964197555cc26` (us-east-1d, t4g.medium, arm64, Ubuntu 26.04). Region `us-east-1` for everything below.

## 0. Confirm first
- [ ] Is this the same AWS account as the production loan platform? If yes, every resource below stays separate from it (own role, own SG, own bucket, own key pair), and none is shared.

## 1. S3 bucket
- [ ] Name: `argon-prod-backups-<ACCOUNT_ID>`. It must be globally unique; tell us the final name.
- [ ] Region: `us-east-1`
- [ ] Block Public Access: all four settings **on**
- [ ] Object Ownership: bucket owner enforced (ACLs disabled)
- [ ] Versioning: **enabled**
- [ ] Default encryption: SSE-S3 (`AES256`). The scripts send `--sse AES256`.
- [ ] Bucket policy: deny any request where `aws:SecureTransport` is `false`
- [ ] Object Lock: **your decision.** If wanted, it must be enabled at creation, in compliance mode for 35 days on the `prod/dumps/` prefix. Nobody, including root, can then delete a recent backup.
- [ ] Lifecycle on `prod/dumps/`: expire current versions after 90 days, noncurrent versions after 30 days
- [ ] Lifecycle on `prod/drills/`: expire after 365 days
- [ ] No other principal gets access besides the role in §2 and the operator in §4

## 2. IAM role for the instance
- [ ] Role name: `argon-prod-host`
- [ ] Trust policy: `ec2.amazonaws.com` may `sts:AssumeRole`
- [ ] Inline policy `argon-backup`: the contents of `deploy/iam/argon-backup-policy.json`, with `CHANGE-ME-argon-backups` replaced by the bucket name from §1. It allows:
  - `s3:ListBucket` on the bucket, condition `s3:prefix` like `prod/*`
  - `s3:PutObject` and `s3:GetObject` on `arn:aws:s3:::<bucket>/prod/*`
  - **no `s3:DeleteObject`, no `s3:PutBucket*`, and nothing outside `prod/`**
- [ ] Managed policy `AmazonSSMManagedInstanceCore` (optional). It lets port 22 be closed; the SSM agent is already running on the host.
- [ ] Instance profile `argon-prod-host`, attached to `i-0df0964197555cc26`. The instance currently has **no** instance profile.

## 3. Security group
- [ ] New SG `argon-prod-host`, replacing `launch-wizard-3` on `i-0df0964197555cc26` (current rules unknown to us)
- [ ] Inbound TCP 22: from `<operator public IP>/32` only. If §2's SSM policy is attached, no inbound rules at all.
- [ ] Inbound TCP 5432: **none**. Postgres is bound to 127.0.0.1 and reached only over an SSH/SSM tunnel.
- [ ] Inbound TCP 8080: **none**. The API sits behind a TLS proxy, which isn't yet decided.
- [ ] Inbound TCP 443: none for now. Added when TLS is decided.
- [ ] Outbound TCP 443 to `0.0.0.0/0`: S3, SSM, PGDG apt, go.dev, snap store
- [ ] Outbound TCP 80 to `0.0.0.0/0`: the Ubuntu apt mirror (`us-east-1.ec2.archive.ubuntu.com` serves http)
- [ ] Nothing else outbound needed. DNS and NTP use the VPC resolver and Amazon Time Sync, which security groups don't filter.

## 4. Operator credentials for the machine that runs the scripts
- [ ] Current local credentials fail (`SignatureDoesNotMatch`): the secret key is wrong or rotated. Issue new ones, or SSO.
- [ ] IAM user or SSO permission set `argon-operator`, CLI profile name `argon`:
  - `ec2:DescribeInstances`, `ec2:DescribeSecurityGroups`, `ec2:DescribeIamInstanceProfileAssociations`
  - `s3:ListBucket` on the bucket; `s3:GetObject` on `<bucket>/prod/*` (to verify that backups land)
  - `ssm:StartSession` on `i-0df0964197555cc26` only, if §2's SSM policy is used
  - **no write access to anything**
- [ ] MFA required on this identity

## 5. SSH key
- [ ] New key pair `argon-prod-host`, for this instance only. It replaces `prod-loan-wallet`, which is currently the only authorized key.
- [ ] Confirm whether `prod-loan-wallet` opens any other host, in particular the loan platform. If it does, **rotate** it there too.

## 6. Send back to us
- [ ] Bucket name
- [ ] Role / instance-profile ARN
- [ ] SG ID
- [ ] Operator IP used in the SSH rule
- [ ] Profile name
- [ ] Whether Object Lock was enabled
