---
name: jwt-attacks
description: Attack JSON Web Tokens (JWT) used for authentication or authorization. Use when a service issues or accepts bearer/JWT tokens, to test for signature, algorithm, and claim weaknesses that grant auth bypass or privilege escalation.
---

# JWT attacks

Goal: turn a captured token into an auth bypass or a higher-privilege token.

## Triage the token

- Decode the header and payload (base64url). Note `alg`, `kid`, issuer, subject,
  roles/scopes, and expiry.
- Record the token and its claims as a `cred` fact.

## Test, in order

1. `alg: none` — strip the signature and set `alg` to `none`/`None`; some
   libraries accept an unsigned token.
2. Algorithm confusion (RS256 -> HS256) — if the server verifies RS256, try
   re-signing with HS256 using the public key as the HMAC secret.
3. Weak HMAC secret — for HS256, attempt an offline dictionary/brute crack of
   the signing key, then forge tokens at will.
4. `kid` injection — path traversal or SQL in the `kid` header to point
   verification at an attacker-controlled or predictable key.
5. Claim tampering — after any of the above, escalate by editing `role`,
   `admin`, `sub`, `aud`, or expiry claims.
6. Reuse / no-expiry — check whether old or captured tokens still validate.

## Record

- `vuln` fact naming the exact weakness and the forged token that proves it.
- `foothold` / `cred` fact for the resulting privileged token; spray it against
  every API that trusts the same issuer.
