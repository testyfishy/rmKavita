# Installing rmKavita on a reMarkable Paper Pro

rmKavita is an unofficial AppLoad app: not affiliated with or endorsed by Kavita or reMarkable.

## What you need

- A reMarkable Paper Pro with xovi and AppLoad 0.5.0 or newer (0.5.0 added the RGBA8888 framebuffer the app asks for).
- SSH access to the tablet with a key (`root@<tablet>`), from a computer with a POSIX shell: Linux, macOS, Git Bash or WSL.
- The package folder: the static `linux/arm64` binary `rmkavita` built from this repository, plus
  `external.manifest.json`, `icon.png` and `rmkavita.conf.sample`.
- A Kavita server the tablet can reach. You sign in on the tablet with your Kavita user name and password;
  the installer never sees them.

## Install

```sh
cp install.conf.sample install.conf      # edit TABLET_HOST, SSH_KEY, PACKAGE_DIR, and the Kavita address
./install.sh --dry-run                   # prints the plan, runs only read-only checks, changes nothing
./install.sh                             # asks once; --yes skips the question
```

Then tap AppLoad's reload button on the tablet: "rmKavita" appears. The first launch shows the Sign-in screen.
The installer does not restart xochitl or AppLoad.

What it does: checks the package (static AArch64 ELF, manifest, nothing key-shaped), looks at the tablet read-only
(model, AppLoad, free space, whether KOReader or rmKavita is running: it refuses unless you pass `--force`), uploads
through ssh, places the app folder with renames (the previous folder is put back if the swap fails), writes the
configuration from the sample only when there is none, runs `rmkavita -migrate`, and finishes with a check that only
counts: config mode 600, one `server =` line, at most one `api_key` line (with a `username` line) and nothing else
that looks like a key. Re-running it is safe: a signed-in configuration is kept.

## The optional door

If your Kavita server is only reachable through Tailscale, `DOOR=yes` installs a small systemd socket on
`DOOR_LISTEN` (default `127.0.0.1:5001`) that forwards each connection with `tailscale nc KAVITA_HOST KAVITA_PORT`.
The app then talks to `http://127.0.0.1:5001`. This needs the root file system writable for a moment (the units live
in `/etc/systemd/system`). The standard device layout is Vellum's: its helpers are in `/home/root/.vellum/bin`
(`tailscale`, `mount-rw`, `mount-restore`); the defaults in `install.conf.sample` point there and every one of them
is a setting. With `DOOR=no` nothing outside the app folder, the config and the staging folder is touched.

## Uninstall

```sh
./install.sh --uninstall                 # app folder, config, settings, door units, staging
./install.sh --uninstall --purge         # ... and your reading positions
```

## Troubleshooting

- "tablet is not reachable": the tablet sleeps with its Wi-Fi; wake it, check `TABLET_HOST` and `SSH_KEY`.
  `ssh -p PORT -i KEY root@HOST true` must work without a prompt (the installer uses `BatchMode`).
- "REFUSED: KOReader or rmKavita is running": close it on the tablet, or use `--force`.
- AppLoad version `unknown` in the plan: the installer could not read a version out of `appload.so`; the app negotiates
  the framebuffer at start and falls back to rgb565. Set `fb_format = rgb565` in the app's config to skip the attempt.
- The final check lists problems as counts: a second `api_key` line, a key-shaped line outside it, a wrong mode.
  Nothing from the config is ever printed.

## Tests

`sh test-install.sh` (or `sh test-install.sh dash`) runs the installer against a fake `ssh` and a folder-tree device.
It never touches a tablet.
