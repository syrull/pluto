---
name: cred-spray
description: Spray discovered credentials across every known authentication surface. Use whenever a credential, password, hash, key, or token is looted — password reuse is the highest-value pivot, so try each secret everywhere before moving on.
---

# Credential spray

Goal: convert one discovered secret into access everywhere it is reused. This is
the single highest-value pivot in most engagements — automate it.

## Inputs

- Every `cred` fact on the blackboard: plaintext passwords, hashes, API keys,
  SSH keys, JWTs, and any username enumerated along the way.

## Surfaces to spray

Enumerate auth surfaces from the `service` facts and try each credential against
all of them:

- SSH (`22`), RDP (`3389`), SMB/WinRM, databases (MySQL/Postgres/Mongo/Redis),
  web login forms and basic-auth endpoints, VPN portals, mail (IMAP/SMTP),
  container registries, and cloud/API tokens.
- Try username == password, and every discovered username paired with every
  discovered password.

## Rules

- Respect lockout: spray one credential across many hosts (low and slow per
  account) rather than many passwords against one account.
- On any success, record a `foothold` fact (value = surface + identity) and,
  if the login yields new secrets, loop back with those as fresh spray inputs.
- Stay in scope: only spray surfaces inside the engagement manifest.

## Escalate

- Reused admin credentials often unlock privilege escalation directly. After a
  successful spray, re-run recon from the new vantage point — internal services
  are frequently softer than the perimeter.
