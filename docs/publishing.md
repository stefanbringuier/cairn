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

The repository already has a `v0.1.0` tag and release. Main-branch CI must finish
successfully before the release workflow runs. It checks that the tested commit
is still the current main tip, then computes the next version from commits:

| Commit | Version change |
|---|---|
| `fix:` | Patch |
| `feat:` | Minor |
| `feat!:` or `BREAKING CHANGE:` | Major |
| `docs:`, `test:`, `ci:`, `chore:` | No release |

GoReleaser builds both architectures, includes documentation and images,
generates `checksums.txt`, and publishes the archives. Public releases also
include GitHub build attestations. Tags and releases use the workflow's scoped
GitHub token; contributors do not need publishing credentials.

If publication fails after a tag has been created, use the release workflow's
manual run with that existing `vX.Y.Z` tag. This retries publication without
inventing another version. The workflow validates the tag and reruns checks;
older tags without the Makefile run Go race tests, vet, and module checks.

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
3. In **Settings → Pages**, choose **GitHub Actions** as the source.
4. Add the repository Actions variable **`PAGES_ENABLED` = `true`**.
5. Run the **pages** workflow manually, or push a docs change to `main`.

The deployment environment is `github-pages`. Require maintainer approval there
if desired. PRs build and validate documentation without deploying it. The
expected project URL is `https://stefanbringuier.github.io/cairn/`; it is not live
until Pages is enabled and a deployment succeeds.

To stop automatic site publication, set `PAGES_ENABLED` to `false`. This stops
future deployments; remove an existing published site separately in Pages
settings if needed.
