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
fails before publishing anything.

To preview the notes for a version before tagging:

```sh
awk -v hdr="## [X.Y.Z]" '
  index($0, hdr) == 1 { found = 1; next }
  found && (index($0, "## [") == 1 || /^\[[^]]+\]: /) { exit }
  found { print }
' CHANGELOG.md
```

## Upgrade safety

Config, profiles, and state live in XDG directories and outlive the binary, so
a release has to keep reading what earlier ones wrote. Profiles in the field may
contain values no current code path produces. Before tagging, check that changes
to profile parsing, `EXTRA_ARGS` handling, or the service unit still work
against a profile written by the previous release, and note anything that
doesn't under `Changed`.
