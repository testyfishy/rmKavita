# Security policy

## Reporting a problem

Please report a security problem **privately**: use GitHub's "Report a vulnerability" (the repository's Security
tab, `https://github.com/testyfishy/rmKavita/security/advisories/new`). If that is not available to you, open an issue that says only that you
have a security report and how to reach you, without details, and the maintainer will arrange a private channel.

Include the version (Settings -> About, or `rmkavita -version`), what you did and what happened. Do not include a
real Kavita key, password or library content in a report: describe where it would be.

You will get an answer as soon as the maintainer can; this is a one-person project without a service level.

## Supported versions

The latest release.

## What the program keeps, and where

- **The Kavita API key (the OPDS key)** lives in `/home/root/.config/rmkavita.conf` on the tablet, mode 0600, on
  one `api_key` line next to `server` and `username`. The file is written atomically; the installer's final check
  verifies mode 600.
- **The password** is typed on the tablet's Sign-in screen, held in memory only until the key is obtained, and
  wiped. It is never written to a file or to the log.
- **Positions and settings** are small JSON files next to the config; they contain no key.
- **The log** (`/tmp/rmkavita.log`) never contains a key, password or URL: log lines go through a redactor and a
  test fails if one slips through.
- **The installer** (`install/install.sh`) never asks for, reads, prints or stores a key or password; it
  only counts what it finds in the config (one `server` line, at most one `api_key` line, mode 600).
- **No telemetry.** The app talks only to the Kavita server you configured.

The app speaks plain HTTP to the server address you give it; if your server is reachable over an untrusted
network, put it behind TLS or a VPN. A key stored on the tablet can be revoked in Kavita (regenerate the OPDS
key of the account).
