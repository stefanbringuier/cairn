# Developing cairn

cairn is a small Linux CLI and TUI that gives people a way back to their files.
Keep the commands short, the defaults useful, and the implementation thin.
restic stores and restores data; rclone reaches remote destinations; systemd
user timers schedule runs. Use those tools instead of building replacements.

## Read before changing behavior

- [docs/spec.md](docs/spec.md) defines the command and storage contract.
- [docs/design.md](docs/design.md) explains architecture and extension points.
- [docs/usage.md](docs/usage.md) explains the product to users.
- [CONTRIBUTING.md](CONTRIBUTING.md) defines the PR and validation workflow.

Every PR includes the relevant documentation update. Behavior changes update
the specification and design notes alongside the implementation. Changes to
commands, flags, output, or exit codes also update the embedded
[agent reference](internal/app/skill.md). Keep examples executable.

## Code and terminal experience

The entry point is `cmd/cairn`; implementation and colocated tests belong in
`internal/app`. Add packages when they create a clear boundary, not just to
make more folders. Prefer standard Go and small functions over frameworks.

Keep JSON output stable, free of terminal styling, and useful without a TTY.
Explain commands and keys in plain language. Follow the shared palette and
stacked-stone mark. Check resizing, long paths, filtering, and keyboard-only
navigation. TUI changes include fresh captures of the actual running program.
Label setup examples accurately; an example is not a connected cloud account.

Preserve preview, confirmation, pre-restore snapshots, and the printed undo
command. `-y` skips prompts but keeps the safety snapshot. Without a TTY, an
operation that needs confirmation exits 2. Pass repository paths after `--`,
sanitize terminal text, and keep credentials out of errors and screenshots.
Use disposable repositories and isolated XDG directories for integration tests.

Agent inspection defaults to `history`, `diff`, and `recover` into new copies.
Never infer replacement approval from `-y`, a broad recovery request, or an
agent-controlled prompt. Complete safety backups include normally excluded
files and existing `--to` destinations; partial backups abort replacement.
Missing destinations use atomic no-replace publication. Skills and CLI guards
cannot constrain unrestricted same-user shell access; document external
permissions honestly. See [agent safety](docs/agent-safety.md).

## Validate and contribute

Run `make setup` once, then `make fmt` and `make check vet`. Add focused tests
for behavior changes; use `make integration` for restore or storage changes.
Run `make cross-check` when changing platform-dependent code, and `make docs`
when changing documentation or the site. See CONTRIBUTING.md for optional tools.

Work on a branch and submit a PR to `main`; do not push changes directly to
`main`. Use Conventional Commit subjects and PR titles, since PRs merge by
squashing. Contributor identity belongs to the actual author; integration
committers on `main` are the maintainer or GitHub's squash-merge identity.
Do not add attribution trailers automatically. Do not add vendor-specific
agent dotfolders; this root document is the common directive.

Keep releases statically linked for Linux amd64 and arm64. Do not change
visibility, push tags, publish releases, or enable Pages as part of routine
development. Those are maintainer actions with explicit authorization.
Merging a PR publishes a release after main CI succeeds, so merging also needs
maintainer authorization. Patch releases are the default; features and breaking
changes select larger versions. See docs/publishing.md for gates and retries.
