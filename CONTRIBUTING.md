# Contributing to rmKavita

Thank you for looking. rmKavita is small on purpose: one static binary that streams page images from a Kavita
server to an e-ink panel. The goal and the non-goals are in [ROADMAP.md](ROADMAP.md); a change that does not
make streaming images in series more comfortable or more efficient is probably out of scope. Opening an issue
before a larger change saves everyone time.

## Licence of contributions

rmKavita is source-available under the [PolyForm Noncommercial License 1.0.0](LICENSE). By submitting a
contribution (a pull request, a patch, or code in an issue) you state that you wrote it or have the right to submit
it, and you agree that:

1. it is licensed under the project licence (PolyForm Noncommercial 1.0.0), **and**
2. the maintainer may relicense it, including under commercial terms, as part of the project.

Say so in the pull request ("I agree to the contribution terms in CONTRIBUTING.md"). **Contributions without that
statement are not merged.**

## Name and icon

"rmKavita" and the icon are the author's. They are not licensed for use as the name of another product or fork;
a fork should use a different name and icon and keep the credits.

## Ground rules

- **No copied code from other projects.** KOReader, Plato, Kavita and others are AGPL / GPL: ideas are welcome,
  code is not. Say where an idea came from in the pull request so it can go into [CREDITS.md](CREDITS.md).
- **No secrets, no real library content, no personal data** in code, tests, fixtures, logs or screenshots. Use the
  mock server (`internal/mockkavita`) and the generated pages.
- A key, password or URL must never reach a log line. The redaction test must keep passing.
- Go only, no cgo, standard library plus `golang.org/x/image` and `golang.org/x/text` (already linked).
  A new dependency needs a reason in the pull request and its licence in `NOTICE`.
- Keep the binary small and the idle cost zero: no background work while idle, no telemetry.

## Build and test

```sh
gofmt -l .                              # must print nothing
go vet ./... && go test ./...
GOOS=linux GOARCH=arm64 go vet ./...
./build.sh                              # package/rmkavita
./release.sh                            # reproducible build, SHA256SUMS and tarball in dist/
```

Tests need no tablet and no Kavita. UI behaviour is tested with the mock server and a fake display; the message
sequences that go to the panel are checked with a fake AppLoad peer, so a change that adds an update or a mode
message to a page turn or to the bars fails a test on purpose. New behaviour comes with a test.

Screenshots in `docs/screenshots` are rendered from the synthetic fixtures with the PNG backend
(`-backend png`); regenerate rather than edit them.

## Pull requests

Small, one purpose, with a sentence on what you tried on a real panel if the change is about the display. The
maintainer reads on-device behaviour more closely than style.
