# Security

## Reporting a vulnerability

Please report it privately through [GitHub's vulnerability reporting](https://github.com/stefanbringuier/cairn/security/advisories/new). Don't open a public issue. You'll get a reply within a week, and a fix or mitigation plan once the issue is confirmed.

Only the latest release is supported.

## How cairn handles security

- **No sudo by default.** cairn runs as you. The only privileged commands are the package reinstall steps in `cairn rebuild`. Each one is shown in full and only runs after you confirm it (or pass `-y`).
- **Pinned downloads.** If restic or rclone is missing, cairn downloads one exact release from GitHub. It checks the file against a SHA256 hash compiled into cairn, so a release that was swapped after the fact is refused.
- **Repo contents are treated as untrusted.**
  - Snapshot paths and names are passed to restic after `--`, so they can't act as options.
  - Control characters are stripped before anything is printed.
  - Package lists restored from a backup must be plain package names before they reach `sudo apt install` and similar commands.
- **No secrets in argv.** The repository location goes to restic through its environment, and a password through a file (mode 0600). Credentials in URL targets are hidden whenever cairn prints the target.
- **Passwords are off by default.** That's a deliberate choice for simple local and NAS setups: anyone who can read the target can read your files. For cloud or shared targets, use `cairn init <target> --password`.

## Supply chain

- Every GitHub Action is pinned to a commit SHA.
- Workflows run with least-privilege tokens.
- CI runs `go vet`, the race detector, golangci-lint with gosec, and govulncheck (also on a weekly schedule).
- Releases are built by GoReleaser with `-trimpath` and ship a `checksums.txt`. On the public repo they also carry GitHub build provenance (`gh attestation verify`).
