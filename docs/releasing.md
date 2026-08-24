# Releasing

The release is entirely goreleaser: push a tag, the `Release` workflow builds
every target, publishes a GitHub release with checksums, and updates the
Homebrew cask. Nothing is built or uploaded from a developer machine.

## One-time setup

### 1. The tap repository

Create a **public** repository named `homebrew-tap` under the same account as
this one (`Onizuka893/homebrew-tap`). The name matters: Homebrew maps
`brew install Onizuka893/tap/lazymqtt` onto `github.com/Onizuka893/homebrew-tap`
by convention, so users never have to `brew tap` first. It can be empty — the
first release commits `Casks/lazymqtt.rb` into it.

### 2. The tap token

The workflow's default `GITHUB_TOKEN` is scoped to *this* repository and cannot
push to the tap. Create a fine-grained personal access token with
**Contents: read and write** on `homebrew-tap` only, and add it to this
repository as the secret `HOMEBREW_TAP_GITHUB_TOKEN`.

If the secret is missing, the release still publishes and only the cask step
fails — an annoying half-done state, so check it before the first tag:

```sh
gh secret list
```

## Cutting a release

1. `main`/`master` is green in CI, including the `integration` job. The release
   workflow re-runs `go test -race ./...` but *not* the integration suite: a
   tag build does not stand up brokers.
2. `make release-check` — validates `.goreleaser.yaml` locally. The
   `release-dry-run` CI job does the full snapshot build on every push, so a
   broken config is normally caught before you get here.
3. Confirm `STATUS.md` and `docs/configuration.md` describe what actually
   ships. In particular the "accepted but not yet implemented" config keys:
   the schema is frozen by a release in a way it is not by a commit.
4. Tag and push:

   ```sh
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

5. Watch it: `gh run watch`.

The changelog is generated from commit subjects, grouped into Features
(`feat:`) and Fixes (`fix:`), with `docs:`, `test:`, `chore:`, `ci:` and merge
commits excluded — so a change that should appear in the notes needs a
conventional-commit prefix.

## Verifying a release

```sh
brew install Onizuka893/tap/lazymqtt && lazymqtt --version
go install github.com/Onizuka893/lazymqtt/cmd/lazymqtt@v0.1.0 && lazymqtt --version
```

Both must print the tagged version rather than `dev`: the goreleaser build
injects it via ldflags, and the `go install` path falls back to
`debug.ReadBuildInfo`. `internal/version` has tests for both, but this is the
end-to-end check that the ldflags paths in `.goreleaser.yaml` still name the
right symbols — a renamed variable fails silently as `dev`.

Then download one archive from the release page and check it against
`checksums.txt`.

## Versioning

`v0.x` until the config schema is stable, and the README says so. A change to
the config schema before `v1.0.0` is allowed but should be a minor bump and
appear in the release notes. Prereleases (`v0.2.0-rc.1`) are detected
automatically: goreleaser marks the GitHub release as a prerelease and skips
the tap update, so `brew install lazymqtt` never resolves to one.

## If a release goes wrong

`release.mode` is `replace`, so re-running the same tag replaces its assets
rather than appending to them:

```sh
git tag -d v0.1.0 && git push --delete origin v0.1.0
git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0
```

That is fine within minutes of a bad tag. Once anyone has installed it, cut
`v0.1.1` instead — Homebrew and Go module proxies both cache aggressively, and
a moved tag is worse than a wasted patch number.
