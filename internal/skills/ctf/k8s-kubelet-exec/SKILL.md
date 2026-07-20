---
name: k8s-kubelet-exec
description: Exploit an exposed Kubernetes kubelet API for command execution and cluster loot. Use when recon finds a kubelet (typically TCP 10250) that permits unauthenticated or weakly-authenticated access, to list pods, exec into containers, and harvest service-account tokens.
---

# Kubelet exec

Goal: turn an exposed kubelet into container command execution and then into
cluster credentials.

## Confirm the surface

- The read-only port (10255) leaks pod specs and env; the full API (10250) can
  exec. `curl -sk https://<host>:10250/pods` — if pods list without a client
  cert, exec is likely open.
- Record the kubelet as a `service` fact and, if open, a `vuln` fact.

## Execute

1. Enumerate namespaces, pods, and containers from `/pods`.
2. Exec into a pod via the kubelet `run`/`exec` endpoint (namespace, pod,
   container as query params) and run a command.
3. Record the resulting shell as a `foothold` fact (value = pod/container).

## Loot

- Read the mounted service-account token and CA from
  `/var/run/secrets/kubernetes.io/serviceaccount/` inside a pod.
- Read env for injected secrets and connection strings.
- Record every token/secret as a `cred` fact.

## Escalate

- Use a looted service-account token against the API server; check its RBAC
  (`can-i --list`). A token that can create pods or read secrets cluster-wide is
  a path to cluster-admin.
- Spray discovered credentials across other pods and nodes.
