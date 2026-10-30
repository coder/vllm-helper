# Releasing

Release notes come from `CHANGELOG.md`. Tagging publishes whatever that file
says about the version, so the notes are written and reviewed alongside the
change instead of being assembled from commit subjects afterwards.

## While working

Add an entry under `## [Unreleased]` in the same PR as the change, in the
section that fits: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, or
`Security`. Write for someone deciding whether to upgrade — what changed for
them, and what breaks if they skip it. Mention the symptom, not the function
name, for anything they might already be hitting.

Keep it short — a couple of lines, and a third only when there is something the
reader has to act on. One concrete number often does the work of a paragraph of
explanation. The reasoning behind a change belongs in the commit message and the
PR, where it is still there for whoever needs it; the changelog is read by
someone deciding whether to upgrade.

Purely internal work (refactors, tests, CI) doesn't need an entry.

## Cutting a release

1. Pick the version. This project is pre-1.0, so a breaking change bumps the
   minor and everything else bumps the patch.
2. In `CHANGELOG.md`, rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`,
   add a fresh empty `## [Unreleased]` above it, and update the link
   definitions at the bottom of the file.
3. Merge that to `main`.
4. Tag it and push:

   ```sh
   git tag -a vX.Y.Z -m "vX.Y.Z" && git push origin vX.Y.Z
   ```

The `release` workflow extracts the matching section, then GoReleaser builds
linux/amd64, linux/arm64, and darwin/arm64 and publishes the release with that
section as the body. If the tag has no section in `CHANGELOG.md` the workflow
fails before publishing anything, and if the published body comes back empty it
fails after — extracting the notes and publishing them are separate steps, and
v0.4.0 went out with an empty body from a run where the extraction was fine.
Nothing in `.goreleaser.yaml` may set `changelog.disable`: it discards the file
passed to `--release-notes` as well.

To preview the notes for a version before tagging:

```sh
awk -v hdr="## [X.Y.Z]" '
  index($0, hdr) == 1 { found = 1; next }
  found && (index($0, "## [") == 1 || /^\[[^]]+\]: /) { exit }
  found { print }
' CHANGELOG.md
```

## The dev channel

`dev-build` republishes `main` as a single prerelease tagged `dev`, so people
can run unreleased work without waiting for a tag. It runs after `ci` succeeds
rather than on the push, so a commit whose tests fail is never handed to
anyone, and it replaces the release each time rather than adding to it —
asset names carry the version, so uploading would pile up one set per commit.

Dev builds are GoReleaser snapshots: the same archives and checksums as a
release, versioned `X.Y.Z-dev.<short-sha>` from the _next_ patch number. That
string sorts below the release it follows under semver, which is why
`upgrade` compares the commit rather than the version on that channel. CI
writes the commit into the release body, because a tag that moves can't
identify what is inside it.

Nothing about tagging changes: `release` still fires only on `v*`.

## Upgrade safety

Config, profiles, and state live in XDG directories and outlive the binary, so
a release has to keep reading what earlier ones wrote. Profiles in the field may
contain values that no current code path produces. Before tagging, check that
changes to profile parsing, `EXTRA_ARGS` handling, or the service unit still
work against a profile written by the previous release, and note anything that
doesn't under `Changed`.

`upgrade` records what it installed in `state/installed-version`, so a release
that does need to change something on disk can tell where the user is coming
from. There is no migration machinery yet, and deliberately so: the first
change that needs one should add exactly what it needs, in `runUpgrade` between
replacing the binary and recording the new version. Anything written there has
to cope with the file being absent, which is the case for every install that
predates it and for anyone who replaced the binary by hand.
