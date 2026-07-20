---
name: web-fingerprint
description: Fingerprint a web service and map its attack surface. Use on any HTTP/HTTPS service found in recon to identify the stack, discover content and endpoints, and match known exploits before attacking.
---

# Web fingerprint

Goal: identify the web stack and surface, then hand a ranked list of candidate
weaknesses back to the orchestrator.

## Steps

1. Identify the stack.
   - Headers and cookies: `curl -sSIk https://<host>:<port>/` — note `Server`,
     `X-Powered-By`, framework cookies, redirects.
   - Landing page + error pages for framework/CMS tells (favicon hash, meta
     generator, stack traces).
2. Discover content and endpoints.
   - Directory/file discovery with a wordlist; look for `/api`, `/admin`,
     `/.git`, `/actuator`, `/swagger`, `/graphql`, backup and config files.
   - Enumerate virtual hosts if the IP serves several names.
3. Map inputs and auth.
   - Login forms, token endpoints, file upload, SSRF-prone fetchers, and any
     parameter that reaches a template, query, or the filesystem.
   - If the app issues JWTs or bearer tokens, hand off to `jwt-attacks`.
4. Match known exploits.
   - Pin exact product + version, then match to a known CVE/exploit rather than
     fuzzing blind.

## Record

- `service` fact refined with the identified stack/version.
- `vuln` fact per candidate weakness (value = short name, detail = evidence +
  the exact endpoint).
- `cred` fact for any secret, default credential, or token found. Every
  credential is a spray candidate — hand it to `cred-spray`.
