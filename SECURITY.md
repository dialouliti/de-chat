# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in deChat, please **do not** open a
public GitHub issue.

Instead, report it privately via GitHub's "Report a vulnerability" feature
on the **Security** tab of the repository (the recommended path), or email
the maintainer directly. Include:

- A description of the issue and its potential impact.
- Steps to reproduce (proof of concept, logs, or screenshots).
- The version / commit you tested against.

You should receive an initial response within a few days. Please do not
disclose the issue publicly until a fix has been released.

## Scope

This policy covers the deChat server (Go) and the web client (`web/`).
Out-of-scope: vulnerabilities in third-party dependencies (report those
upstream), self-hosted deployments with insecure custom configurations, and
issues that require already having administrative access.

## Deployment hardening

When deploying deChat, operators are responsible for:

- Setting a strong, unique `JWT_SECRET` (at least 32 bytes; the server
  refuses to start otherwise).
- Putting the server behind TLS (a reverse proxy such as nginx/Caddy).
- Configuring `CORS_ORIGIN` to the actual client origin(s) rather than the
  default `*`.
- Securing the PostgreSQL instance and its credentials.
- Redacting the `?token=` query parameter from any access logs produced by
  the reverse proxy in front of the WebSocket endpoint.

## Known dependency vulnerabilities (accepted risk)

This module deliberately pins **Go 1.21** and holds several dependencies at
versions compatible with that toolchain (see `AGENTS.md`). Two CVEs remain as
a result, each assessed as low practical risk for this application:

| Dependency | Pinned | Fixed in | CVE | Assessment |
|---|---|---|---|---|
| `github.com/jackc/pgx/v5` | v5.5.5 | v5.9.0 (needs Go 1.25) | CVE-2026-33815 / CVE-2026-33816 (memory-safety / use-after-free in the driver) | Exploitation requires a malicious or compromised **Postgres server** sending crafted data to the client. This is not SQL injection or RCE. For a private-group deployment where the operator controls the database, the practical risk is low. Upgrading requires bumping the toolchain to Go 1.25. |
| `golang.org/x/crypto` | v0.21.0 | newer (needs Go 1.23+) | CVE-2025-22869 (SSH server DoS) | **Unreachable.** This codebase imports `golang.org/x/crypto` **only** for `bcrypt` (verified: a single import in `internal/auth/auth.go`). No `golang.org/x/crypto/ssh` import exists anywhere, so the vulnerable SSH code is never compiled into the binary. |

`golang-jwt/jwt/v5` was upgraded to **v5.2.2** (CVE-2025-30204, header-parsing
DoS) — this fix is compatible with Go 1.21 and is applied.

If this deployment model changes (e.g. untrusted Postgres, or the SSH package
becomes reachable), re-evaluate and bump these dependencies along with the
Go toolchain.
