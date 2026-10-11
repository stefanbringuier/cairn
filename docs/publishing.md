# Releases and publishing

## Linux support

cairn releases one static binary for Linux amd64 and one for Linux arm64, with
`CGO_ENABLED=0`. There is no dependency on a particular distribution's libc.
Use amd64 for ordinary Intel/AMD machines and arm64 for 64-bit ARM machines.

Automatic scheduling needs systemd user timers, a working user session, and
linger support. Container startup checks on Debian, Ubuntu, Fedora, Arch, and
Alpine verify binary compatibility; they do not prove scheduling or recovery.
On an OpenRC-only Alpine machine, use manual snapshot, backup, and restore
commands. `init` saves the setup and warns when user timers are unavailable.
Other architectures can build from source, but need restic and rclone installed
separately and are outside the release support matrix.

## Version tags and releases

New integrations into `main` publish a release after main-branch CI
finishes successfully, including tests, lint, installer and release fixtures,
and the docs build. PR CI runs before merging; main CI tests the resulting
squash commit again. A failed or canceled main run cannot publish a release.
The release workflow checks that the tested commit is still the current main
tip and its latest push CI passed, then computes the next version from commits
since the previous release:

| Commit | Version change |
|---|---|
| `fix:` and all other non-feature changes | Patch |
| `feat:` | Minor |
| `feat!:` or `BREAKING CHANGE:` | Major |

For example, a CI or documentation update after `v0.1.0` releases `v0.1.1`.
Features select `v0.2.0` and breaking changes select `v1.0.0`. CI reruns do not
create another version when the tested commit already has a release tag.
Scheduled checks and the public-launch build do not create releases.
If main advances before publication starts, the obsolete result is skipped;
the next passing main tip includes those commits in its version calculation.
Release jobs serialize and keep the active publication running; a newer pending
run can supersede an older pending run, whose changes enter the next release.

GoReleaser builds both architectures, includes documentation and images,
generates `checksums.txt`, and publishes the archives. Releases built while the
repository is public also include GitHub build attestations. Making an existing
private release public does not retroactively add attestations; its SHA-256
checksums still apply. Tags and releases use the workflow's scoped
GitHub token; contributors do not need publishing credentials.

If publication fails after a tag has been created, use the release workflow's
manual run with that existing `vX.Y.Z` tag. This retries publication without
inventing another version. The tag must be on main history, and its exact commit
must have a successful latest main push CI run. Failed, canceled, or unfinished
reruns block publication even if an earlier attempt passed. The workflow reruns checks;
older tags without the Makefile run Go race tests, vet, and module checks.

## Protect main

All further changes go through PRs. The repository uses squash merges and
Conventional Commit PR titles, preserving the contributor's authorship.
The committed [protection settings](../.github/main-protection.json) require:

- Passing `test`, `arm64`, `lint`, `commits`, `docs`, and `pr-docs` checks from
  GitHub Actions, with the branch up to date with `main`.
- A PR, resolved review conversations, and linear history.
- The same rules for administrators, with force pushes and deletion disabled.

There is no mandatory second reviewer while this is a solo-maintainer project;
the PR and its passing checks are still required. Increase the approval count
when adding maintainers.

Run from a clone using the maintainer's `gh` login:

```sh
sh scripts/protect-main.sh
```

The script applies the committed settings through the GitHub API, using
`checks` with GitHub Actions app IDs for required checks. The legacy `contexts`
field is omitted because sending both forms fails GitHub's request validation.
Check the result in **Settings → Branches**. GitHub supports protected branches
in public repositories on its free plan; private repositories need a paid plan. See
[GitHub's protected branch guide](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches).

## Preview the docs

```sh
make docs-tools
make docs-serve
```

Open `http://127.0.0.1:8000`. The static site renders README, usage, design,
specification, contributor and agent guidance, and security from their original
files. CI builds it and validates local links. Search and assets work beneath
the GitHub project path; there are no hosted font or analytics dependencies.

## Activate GitHub Pages when ready

Keep the repository private until the maintainer approves the public launch.
The prepared workflow does not enable Pages or change repository visibility.
After that approval:

1. Review the specification, examples, screenshots, and independent-development
   footer for material intended to be public.
2. Make the repository public in GitHub settings.
   CI and the docs build run automatically on that change. Apply main protection
   with `sh scripts/protect-main.sh` before opening the repository to changes.
3. Enable **Settings → Advanced Security → Private vulnerability reporting**.
   GitHub offers this feature for public repositories; the private-report form
   is unavailable while the repository is private. See the
   [GitHub setup guide](https://docs.github.com/en/code-security/how-tos/report-and-fix-vulnerabilities/configure-vulnerability-reporting/configure-for-a-repository).
   [SECURITY.md](../SECURITY.md) keeps email as a working fallback.
4. In **Settings → Pages**, choose **GitHub Actions** as the source.
5. Add the repository Actions variable **`PAGES_ENABLED` = `true`**.
6. Run the **pages** workflow manually, or push a docs change to `main`.

The deployment environment is `github-pages`. Require maintainer approval there
if desired. PRs build and validate documentation without deploying it. The
expected project URL is `https://stefanbringuier.github.io/cairn/`; it is not live
until Pages is enabled and a deployment succeeds.

To stop automatic site publication, set `PAGES_ENABLED` to `false`. This stops
future deployments; remove an existing published site separately in Pages
settings if needed.
