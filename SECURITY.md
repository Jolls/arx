# Security Policy

## Reporting a vulnerability

Please report privately — don't open a public issue.

Use GitHub's private vulnerability reporting: the repository's **Security** tab → **Report a vulnerability**.

Include what you found, how to reproduce it, and the version (top entry of [`CHANGELOG.md`](CHANGELOG.md)).

Arx is maintained by one person. Reports are acknowledged on a best-effort basis, with no committed response timeline.

## Scope

In scope: the Arx application (`arx_go/`) and shared library (`arxlib/`).

Arx assumes a trusted, single-shop deployment and binds to localhost. Findings that require local administrator access, or write access to the configured file roots or the database, are low severity by design.
