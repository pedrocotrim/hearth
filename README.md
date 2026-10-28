# Hearth

Hearth is a small household ledger for utility bills. People in a household record a bill, split it, and track who has paid the person who covered the provider.

The server is Go. Pages are server-rendered and updated with HTMX. Money is stored in cents. Each bill keeps the split it was given, so inviting someone later does not rewrite history.

## Run

```bash
go run ./cmd/hearth
```

Open http://localhost:8080, create an account, and start a household.

```bash
go test ./...
```

## Docker

```bash
docker compose up --build
```

The SQLite database lives in the `hearth-data` volume. Copy that file to back it up.

The compose file turns secure cookies off so the app works over plain HTTP on localhost. In production, terminate TLS in front of Hearth, set `HEARTH_BASE_URL` to the public `https://` origin, and set `HEARTH_SECURE_COOKIES=true`.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `HEARTH_ADDR` | `:8080` | Listen address |
| `HEARTH_DATA` | `data/hearth.db` | SQLite file |
| `HEARTH_ENV` | `development` | `production` turns secure cookies on |
| `HEARTH_BASE_URL` | request host | Used when showing an invite link |
| `HEARTH_SECURE_COOKIES` | on in production | `true` or `false` |
| `HEARTH_TRUST_PROXY` | off | Trust `X-Forwarded-For` and `X-Forwarded-Proto` |

## How a bill is split

- **Equal** divides the total across the people included on that bill. Everyone starts included. Leftover cents go to those people in a stable order.
- **Weight** uses whole-number weights. A weight of 0 gives that person nothing.
- **Custom amounts** must add up to the bill exactly.

Until someone is marked as having paid the provider, the bill is a plan and nobody owes anyone. After that, each other person's unpaid share is what they owe the person who paid. Payments can be partial. Pairwise balances on the ledger are netted across bills.

A utility can remember people who should start unchecked. Checking them on a single bill does not change that default.

Someone can be added to a household with only a name. They show up on splits and cannot sign in. An invite to the same email links that person to the new account.

Invite links are single-use, expire after 14 days, and can only be accepted by an account with the invited email. Share the link directly. There is no mailer.

## Sign-in

Hearth is its own OAuth 2.0 authorization server. The browser signs in with email and password, receives an authorization code, and exchanges it for a JWT access token and a refresh token. Both are `HttpOnly` cookies. The access token lasts 15 minutes. The refresh token lasts 14 days and rotates on use.

The signing key is `oauth-ed25519.key` and the first-party client secret is `oauth-client.txt`, both in the same directory as the SQLite file. Public keys are at `/.well-known/jwks.json`. The token endpoint is `POST /oauth/token`.

Sessions from before this change are not valid. Sign in again.
