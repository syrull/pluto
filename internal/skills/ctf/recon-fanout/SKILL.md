---
name: recon-fanout
description: Broad-then-deep enumeration of an engagement scope. Use at the start of a CTF/engagement to sweep hosts and ports, then fan out one deep-recon worker per live service. Covers host discovery, port scanning, and service/version fingerprinting hand-off.
---

# Recon fan-out

Goal: turn a scope (CIDRs / host list) into a deduplicated map of live hosts and
services on the blackboard, fast, by parallelizing.

## Loop

1. Host discovery across the scope. Prefer fast sweeps first, then confirm.
   - `nmap -sn <cidr>` for a ping sweep; fall back to `-Pn` per host when ICMP
     is filtered.
   - Record every live host as a `host` fact (value = IP/name, detail = how it
     was found).
2. Port scan each live host. Start wide and fast, then targeted.
   - Fast: `nmap -p- --min-rate 2000 -T4 <host>` to find all open ports.
   - Then service/version + default scripts on the open set:
     `nmap -sVC -p <ports> <host>`.
   - Record each open port as a `service` fact (value = `host:port proto`,
     detail = product/version banner).
3. Fan out deep recon. Dispatch one worker per interesting service so they run
   in parallel, each scoped to that single service, each appending its own
   findings. Match services to a playbook: HTTP/HTTPS -> `web-fingerprint`,
   token-bearing APIs -> `jwt-attacks`, a kubelet on 10250 -> `k8s-kubelet-exec`.

## Rules

- Deduplicate: check existing `host`/`service` facts before rescanning.
- Stay inside the engagement scope. Never scan a host outside the manifest.
- Every concrete finding becomes a fact — the orchestrator only sees what is
  written down.
