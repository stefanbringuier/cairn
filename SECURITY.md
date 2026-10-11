# Security

## Reporting a vulnerability

Please report it privately by email to
[stefanbringuier@gmail.com](mailto:stefanbringuier@gmail.com). Don't open a
public issue. You'll get a reply within a week, and a fix or mitigation plan
once the issue is confirmed.

When GitHub private vulnerability reporting is enabled, you can also use
**Security → Report a vulnerability** in the repository. Email remains available
if that option is missing.

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
- **Recovery before replacement.** `recover` publishes only new copies. Restores require complete safety snapshots for existing destinations; `-y` cannot skip that backup. Concurrent external edits are not frozen by a snapshot.
- **Agent permissions belong outside the CLI.** Skills and prompts cannot restrict an unrestricted agent running as the owner. Use a separate Linux identity or trusted executor with read-only storage access and a separate recovery area. See [agent safety](docs/agent-safety.md).

## Supply chain

- Every GitHub Action is pinned to a commit SHA.
- Workflows run with least-privilege tokens.
- CI runs `go vet`, the race detector, golangci-lint with gosec, and govulncheck (also on a weekly schedule).
- Releases are built by GoReleaser with `-trimpath` and ship a `checksums.txt`. On the public repo they also carry GitHub build provenance (`gh attestation verify`).
