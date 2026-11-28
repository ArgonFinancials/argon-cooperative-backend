# The VFD relay

## The cooperative does not hold the VFD token

After this relay is installed, **the cooperative's server no longer holds VFD's
token at all.** The token lives in one file on the wallet's server
(`/etc/argon-vfd-proxy/proxy.env`, root-only), and only this relay reads it.

What the cooperative holds instead is a signing key. That key is worth very
little to anyone who steals it:

- It works only from the cooperative's one address (54.85.168.60), because the
  security group and nginx both refuse every other address.
- It opens only six calls (listed below), not the whole of VFD. Five of them
  read; one sends a transfer.
- It is useless anywhere else. It is not VFD's credential, and VFD has never
  seen it.

**Someone who takes the cooperative's server gets six calls from one address,
and nothing they can take away and use.**

Compare the other option: asking VFD to whitelist a second address. That would
put the token itself on the cooperative's server. Anyone who took that server
would hold VFD's full credential, usable from any address VFD accepts, for
every call VFD offers. That difference is why the relay was chosen (decided
2026-11-28).

## What it does

It runs on the wallet's server, the one address VFD accepts. It carries the
cooperative's VFD calls there, adds VFD's token, and sends back VFD's answer
**byte for byte**: the same status, content type and body.

It passes these six calls and nothing else:

| Call | What the cooperative uses it for |
|---|---|
| `GET /transfer/recipient` | name enquiry |
| `POST /transfer` | send a transfer |
| `GET /transactions` | ask what became of a transfer |
| `GET /account/enquiry` | the pool account's details |
| `GET /account/transactions` | an account's history |
| `GET /bank` | VFD's list of banks, for a member choosing their bank (added 2026-11-28) |

**It never interprets, retries or decides.** Every rule about what a VFD answer
means stays in the cooperative's code (`internal/vfd`), tested and with
mutants. A second copy of those rules here would be a second place for them to
drift. In particular:

- A transfer is never sent twice. When VFD does not answer in time, the relay
  says so (`504 vfd_timeout`). The cooperative then *asks* VFD what happened;
  it never assumes.
- It does not check what a call is for. For example, it does not check whether
  a reference starts with COOP-. That is a rule about meaning, and those rules
  live in the cooperative.

## Its own answers are marked as its own

When the relay answers for itself, nothing was asked of VFD. Those answers
carry an `X-Argon-Proxy` header:

| Status | X-Argon-Proxy | Meaning |
|---|---|---|
| 401 | `bad_signature` | Unsigned, or signed with a key it does not hold, or the body changed after signing. |
| 401 | `stale` | Signed more than 60 seconds from the relay's clock. |
| 401 | `replayed` | The same signed call was already made. |
| 403 | `not_allowed` | Not one of the six calls. |
| 502 | `vfd_unreachable` | VFD could not be reached. Nothing was sent. |
| 504 | `vfd_timeout` | VFD took the call and did not answer in time. **It may have acted.** |

The cooperative's client reads any marked answer as "VFD was not reached",
except `vfd_timeout`, which it reads as "unknown".

It never reads a marked answer as VFD's own reply. If it did, a relay 401 could
tell a member their bank details are wrong when nothing was ever asked. The
tests cover this (TestVfdClient23, with a break where the header is ignored).

## Signing

Each call carries two headers:

- `X-Argon-Timestamp`: Unix seconds.
- `X-Argon-Signature`: hex HMAC-SHA256 of
  `METHOD \n PATH \n RAWQUERY \n TIMESTAMP \n hex(sha256(BODY))`.

The relay checks the signature in constant time. It refuses a call signed more
than 60 seconds away from its own clock, and it refuses a signature it has
already seen in the last two minutes. The code is in `internal/vfdsign`, and the
cooperative and the relay share it.

**Clocks.** Every answer carries `X-Argon-Proxy-Time`, the relay's clock. The
cooperative checks it:

- The client logs an ERROR on any call where the two clocks are more than 10
  seconds apart.
- `deploy/67-check-vfd-relay-clock.sh` runs on the freshness timer and raises
  an alert at 10 seconds. That is long before the 60 seconds at which every
  call would be refused.

## The record

The relay writes one JSON line per call to
`/var/log/argon-vfd-proxy/requests.log`, refused calls included. Each line
holds what was asked and what was answered, **verbatim except for the BVN.**

**Any field whose name contains "bvn" has its value replaced before the line
is built.** The redacted copies are made first, the line is built from those
copies only, and it is written in one append. Nothing that carried a BVN ever
reaches the writer, so a crash mid-write cannot leave one behind.

VFD's token is never in the record. The record is kept for 90 days (logrotate).

## Installing (on the wallet's server, as root)

Build the binary:

```
GOOS=linux GOARCH=amd64 go build -o deploy/vfd-proxy/vfd-proxy ./cmd/vfd-proxy
```

Copy the folder to the wallet's server.

1. **DNS.** Point the relay's own name, for example `vfd-relay.example.com`, at
   the wallet server.
2. **Secrets.** Type each one in unseen. Nothing goes on a command line or into
   shell history:

   ```
   sudo bash install.sh --set VFD_BASE_URL
   sudo bash install.sh --set VFD_ACCESS_TOKEN
   sudo bash install.sh --set PROXY_KEYS
   ```

   `PROXY_KEYS` is the key from `openssl rand -hex 32`. The same value goes in
   the cooperative's `deploy.env` as `VFD_PROXY_KEY`.
3. **Install.** Run `sudo bash install.sh --domain vfd-relay.example.com`.

   It creates the `argon-vfd-proxy` user and unit, the logrotate file and the
   nginx site, and it gets a Let's Encrypt certificate. It then reads back each
   step. The relay must answer an unsigned call as `bad_signature`. Nginx must
   refuse the wallet server's own address with 403, on a certificate that
   verifies.
4. **Security group.** Open TCP 8443 to `54.85.168.60/32` only.

**Then, on the cooperative's server**, in `deploy.env`:

- Set `VFD_BASE_URL=https://vfd-relay.example.com:8443`.
- Set `VFD_PROXY_KEY=<the key>`.
- **Remove `VFD_ACCESS_TOKEN`.** `30-install-api.sh` refuses to run with both.

Then reinstall the API (`30-install-api.sh`). The first signed call is the
society's VFD account lookup (`48-society-vfd-account.sh`).

## Rotating the key

1. Generate a new key.
2. On the relay, put it **first** in `PROXY_KEYS` (`new,old`) and restart.
3. On the cooperative's server, set `VFD_PROXY_KEY` to the new key and
   reinstall the API.
4. Remove the old key from `PROXY_KEYS` and restart again.

Between steps 2 and 4 both keys are accepted (TestVfdProxy08).

## Tests

- `test/tier1/tier1_vfdproxy_test.go`: TestVfdProxy01–09. Run with
  `mutate.sh vfdproxy --svc`.
- The 13 breaks are in a reference relay generated from `proxy.go` by
  `genproxyfake.py`, so it cannot drift from it.
- The client's side is TestVfdClient23–26 (`mutate.sh vfd --svc`).
- The clock alert: `test/ops/vfd_relay_clock_test.sh` and
  `sabotage_vfd_relay_clock.sh`.
