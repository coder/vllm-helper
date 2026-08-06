# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Entries for 0.3.1 and earlier were reconstructed from the commit history after
the fact.

## [Unreleased]

### Added

- macOS (Apple Silicon) support. The server is managed with `launchd`, memory
  is read from `sysctl`, running servers are found without `/proc`, and
  `doctor` checks for the cmake/ninja/clang toolchain with `brew` hints. Linux
  keeps systemd, `/proc`, and apt.
- `stop`, `restart`, and `logs` commands. `restart` re-applies profile edits
  without a `switch`.
- `switch`, `new`, and `restart` watch the boot: they poll `/health`, report
  when the model is actually serving, and fail fast on a crashed or
  crash-looping unit. `--wait`, `--no-follow`, and `--timeout` control it.
- `--json` output for `status`, `list`, `search`, and `doctor`, plus a new
  `inspect` command.
- `edit` (opens `$EDITOR`) and `set KEY=VALUE` for changing profiles, both
  validated before they take effect.
- `doctor` verifies Coder sync credentials against the API, and reports whether
  a HuggingFace token is present and whether the Hub accepts it. Search and
  public models never need one; it matters only for gated or private repos.
- CI runs on macOS as well as Linux, and runs the tests with the race
  detector.

### Changed

- Commands are built on [cobra](https://github.com/spf13/cobra). `--help` now
  lists them in groups, each command documents its own flags, and unknown
  commands and flags are reported in cobra's wording. Exit codes are unchanged:
  0 on success and for help, 1 for anything else.
- Long flags must be spelled with two dashes. `--json` always worked and is
  what the README has always shown, but the old flag parser also accepted
  `-json`; the new one reads that as a bundle of single-letter flags. Where the
  letters spell a real flag, the error says which spelling to use. `logs -n`
  still works, as a genuine shorthand for `--lines`.
- An argument too many is now rejected instead of dropped, so
  `vllm-helper status runing` and `vllm-helper switch qwen typo` fail rather
  than quietly reporting on something else or switching and saying nothing.
  Commands that legitimately take several, like `search` and `set`, are
  unaffected.
- `--help` works on `set`, `edit`, `remove`, and `coder-sync`. Those four took
  no flags, so the old parser read `--help` as a bad argument and exited 1
  instead of describing the command.
- `coder-sync register-all` only backfills entries that are missing, instead of
  rewriting ones that already exist.
- Non-interactive sessions never prompt, tail, or block on `sudo`. This makes
  the CLI usable from scripts and from a UI backend.
- `EXTRA_ARGS` is split shell-style, so a quoted value containing spaces stays
  one argument instead of being torn in half. Backslashes and single quotes in
  profile values are escaped correctly when written back out.
- `start` exits cleanly when no model is active yet, rather than failing. The
  service unit is installed before the first `switch`, and a non-zero exit sent
  systemd's `Restart=on-failure` (and launchd's `KeepAlive`) into a crash loop
  with nothing to serve. A config that exists but can't be read is still
  reported as the error it is.

### Fixed

- A profile value that started or ended with an unusual whitespace character
  (a vertical tab, a form feed, a non-breaking space) was written unquoted and
  read back as empty, silently emptying the setting. Reading trims all
  whitespace, so writing now quotes anything the trim would eat.
- A value containing a NUL byte came back with a backslash in its place.
- `adopt` saved a lossy copy of the server it claimed to take over. Only three
  boolean and four valued flags were recognized and the rest were dropped, so a
  server running with `--tensor-parallel-size`, `--dtype`, or
  `--served-model-name` came back configured differently on the next restart.
  `--gpu-memory-utilization` was worse: profiles hardcoded `0.85`, so adopting a
  server tuned to `0.92` quietly reserved less KV cache.
- `adopt` could carry credential-bearing flags into a profile file and into
  `doctor --json`. They are now dropped, matched on the trailing word so real
  flags like `--tokenizer-mode` and `--ssl-keyfile` aren't mistaken for secrets.
- Bare JSON in `EXTRA_ARGS` was mangled by shell-style splitting, turning
  `{"thinking":true}` into `{thinking:true}`. vLLM rejects that, so existing
  profiles would have stopped serving on upgrade.
- `switch` could wait forever. Only a `failed` unit ended the wait, so a service
  that went inactive left it polling a health endpoint that would never answer.
- `doctor --yes` hung on a `sudo` password prompt when there was no terminal to
  answer it.
- `new` clamps `MAX_MODEL_LEN` to the model's own `max_position_embeddings`
  instead of trusting the README.
- `status` reports the host's service manager rather than always saying
  systemd.
- `set` validates the whole batch before writing. It used to apply and print
  each pair as it went, then write and validate last, so a bad pair mid-list
  printed changes that were never saved, and an unparseable `EXTRA_ARGS` reached
  disk before the error surfaced.
- `--` is honored as an end-of-flags terminator.
- On macOS, `--gpu-memory-utilization` is fitted to actually free RAM. vLLM's
  CPU backend reads that fraction against system memory, not VRAM.

## [0.3.1] - 2026-08-03

### Fixed

- Chat templates that raise when a system message appears anywhere but first.
  Long-running conversations insert one mid-thread, which trips it reliably, and
  no vLLM flag controls the behavior. `new` now fetches the model's
  `chat_template.jinja`, patches it to drop a misplaced system message instead
  of raising, and points the profile at the patched copy.

## [0.3.0] - 2026-07-30

### Added

- Thinking mode is detected from the model card: the `deepseek_v3` and
  `poolside_v1` reasoning parsers get
  `--default-chat-template-kwargs {"thinking":true}` automatically, which vLLM
  silently no-ops without.
- `doctor` checks for the build packages vLLM's JIT kernel compilation needs.

### Changed

- **Breaking:** the Go binary is the only implementation. The bash version,
  its installer, the service template, and the curated model profiles are gone.
  Users define their own profiles, and `setup` and `adopt` handle installation.

### Fixed

- Env-file quoting for JSON-valued flags, broken in 0.2.0. Values like
  `--default-chat-template-kwargs {"thinking":true}` now round-trip correctly.

## [0.2.0] - 2026-07-30

### Added

- A Go CLI with full parity to the bash implementation. Both read and write the
  same XDG config, profile, and state files, so they were interchangeable during
  the transition.
- `adopt` detects an already-running vLLM server, saves its live command line as
  a profile, and takes over managing it without a restart. It scans host
  processes with container awareness, enumerates Docker and Podman containers,
  and probes localhost endpoints.
- `doctor` reports a hardware summary, an inventory of running servers, and
  warnings when the config has drifted from what's actually running.
- `setup`, a first-run wizard for binary installs: a hardware-aware model budget
  that distinguishes unified memory from discrete VRAM, vllm detection, secrets
  generation, and systemd unit installation in system or user scope.
- Release pipeline via GoReleaser and GitHub Actions, building linux/amd64,
  linux/arm64, and darwin/arm64 on every tag, plus CI covering gofmt, vet,
  cross-compilation, tests, and shellcheck.

## [0.1.0] - 2026-07-30

### Added

- Initial public release. A CLI for running a single-model,
  OpenAI-compatible vLLM server and switching models through saved profiles,
  with HuggingFace search, flag detection from model cards, and optional Coder
  Agents sync.
- An installable layout: config, secrets, state, and data split across XDG
  directories, with nothing written into the checkout, no hardcoded paths or
  users, and an idempotent `install.sh` supporting system- and user-scope
  systemd units.

[Unreleased]: https://github.com/coder/vllm-helper/compare/v0.3.1...HEAD
[0.3.1]: https://github.com/coder/vllm-helper/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/coder/vllm-helper/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/coder/vllm-helper/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/coder/vllm-helper/releases/tag/v0.1.0
