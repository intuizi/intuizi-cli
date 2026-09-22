# Security policy

## Reporting a vulnerability

Report it privately through
[GitHub's advisory form](https://github.com/intuizi/intuizi-cli/security/advisories/new),
or email **security@intuizi.com**, with what you found, how to reproduce it,
and what an attacker could do with it. Please do not open a public issue for a
security problem, and please give us a chance to fix it before writing about
it publicly.

You should get an acknowledgement within a few working days.

## Scope

This repository holds the Intuizi CLI: a client that authenticates to the
Intuizi API and sends requests on your behalf. Reports that matter most here
are ones about how the CLI handles your credentials, what it sends where, and
anything that lets one user's token reach the wrong host.

Problems in the Intuizi API or the console belong to the same address, but
say which surface you mean.

## Supported versions

The latest release. Fixes go into the next version rather than into patches of
older ones.

## What the CLI does with your token

`intuizi auth login` exchanges your account credentials for an API token and
writes it to `~/.config/intuizi/config.json` with owner-only permissions. The
token is sent only to the console it was minted for, as a bearer header over
HTTPS, and redirects are refused rather than followed. `INTUIZI_API_TOKEN`
overrides the stored token for CI. `intuizi auth logout` forgets the token
locally; revoke it in the console under My Account then API Tokens.
