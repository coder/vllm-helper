# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Entries for 0.3.1 and earlier were reconstructed from the commit history after
the fact.

## [Unreleased]

### Added

- Documentation, served by the binary itself: `vllm-helper serve` now has a
  **Docs** tab with a quickstart, the profile and memory knobs, the Coder
  integration, and a CLI reference generated from the command tree. The same
  Markdown lives in `docs/` and stays readable on GitHub.
- Recipes: launch configurations that have been run and measured, rather than
  inferred. An entry pins the model, the image by digest, the exact argv and
  the numbers it produced on named hardware, and `recipes list` / `recipes show`
  report where every setting came from. `recipes launch` starts the containers
  over SSH on the nodes you name — a recipe may span machines — writes a profile
  of the same name, and points Coder at what is now serving, so the picker and
  the server cannot disagree. `recipes stop` clears them again.
- `status`, `logs`, `restart` and `stop` follow whatever is actually serving.
  They used to assume the init-system service and would report systemd as
  healthy while a container did the work, or print "Stopped vllm.service" about
  a two-node cluster.
- `recipes install-service` installs a unit that keeps the active recipe
  serving, plus a timer that relaunches it when the endpoint stops answering.
  It stands the native unit down, which cannot serve a container, and holds
  off while a launch is in flight: a model takes minutes to load, and a
  watchdog that reads that silence as an outage restarts the previous profile
  over the top of the new one. A deliberate `stop` or `systemctl stop` is left
  stopped. The web panel's buttons do not drive recipes yet; use the CLI.
- Switching between a recipe and a native profile stops the one being left,
  so neither keeps the GPUs and port the other needs.
- Declared deviations. Where an entry differs from the upstream recipe it came
  from, it says so as data — the setting, upstream's value, ours, and why.
  `tools/recipediff` expands the upstream recipe and reports any difference
  that has not been declared, comparing content rather than commit ids so a
  pin that merely trails upstream is not reported as drift.
- More than one machine. `node add` records a machine once, by the URL it
  serves on and the SSH target that drives it; `pool`, `shard` and
  `recipes launch` refer to it by name. `pool serve` puts a router in front of
  replicas serving the same model and keeps a conversation on the node that
  already has its prefix cached; `pool install-service` runs it as a unit.
  `shard start` splits one model across machines with Ray or vLLM's
  multiprocessing executor, for trying a split before there is a recipe for
  it, and `shard install-ray` prepares the nodes. `spark doctor` and
  `spark install-prereqs` cover the build packages a DGX Spark needs.

### Changed

- Coder entries are matched by model rather than by profile name. A renamed
  profile, or a second machine serving the same model, now adopts the existing
  entry instead of adding a second row the picker would offer twice.
- `doctor` warns when the Coder token is within two days of expiring. Coder
  caps token lifetime and an expired token simply stops the picker being
  updated, with no other signal.
- `CODER_MAX_OUTPUT_TOKENS` raises Coder's server-side reply cap for the models
  this machine serves. Unset sends nothing, so whatever is configured in Coder
  stays.

## [0.4.0] - 2026-09-07

### Added

- Choosing which quantization to serve, for the GGUF repos that ship several.
  `new --quant Q8_0` takes one by name, `search` asks with the best fit
  preselected, and the panel offers a dropdown. The choice is written into the
  profile as `MODEL=repo:QUANT`, along with the `--tokenizer` such a model
  needs, so what runs is what was picked rather than whatever vLLM resolves.
- `coder-sync prune` clears model entries this machine can no longer serve. A
  provider names one endpoint, so an entry for a model that isn't here is one
  Coder's picker offers and the server then refuses — and they accumulate, from
  profiles deleted outside vllm-helper or from a provider whose address moved to
  a different machine, leaving the previous one's models behind. It reports by
  default and deletes on `--yes` or a prompt, and calls out an enabled orphan
  separately: a disabled one is untidy, an enabled one is selectable and then
  fails. Entries belonging to other providers in the same deployment are never
  touched.
- `CODER_ORGANIZATION` in `config.env`, for deployments with more than one.
  Coder resolves the literal `default` server-side for single-organization
  deployments, which is why it was hardcoded, but a deployment with several has
  no default to fall back on and had no way to name its own. Still defaults to
  `default`.
- `install-gguf` installs the plugin vLLM now needs to read GGUF at all,
  into the interpreter that owns the configured vLLM rather than whatever
  `pip` is on PATH. It says what the trade-offs are before installing:
  vLLM calls its own GGUF support experimental, the plugin builds from
  source, and a native quant is usually faster where one exists. The panel
  offers the same thing in place of a pull button that could only fail,
  with the command and caveats read from the machine so the two cannot
  drift. Installing disturbs nothing that is running: the plugin is read
  when an engine starts, so it applies from the next switch.
- `vllm-helper serve` runs a web panel for the machine: the model that's
  loaded, whether it's serving or still coming up, how much memory is in use
  against what this hardware can give a model, the other saved profiles with a
  switch button, and the server log. It's a control plane only — inference
  still goes straight to vLLM, so the panel is never in the path of a request
  and can't affect one. Listens on localhost unless `--listen` says otherwise,
  and every request needs the token printed at startup. Anything that controls
  the service — switching, pulling, stopping — needs permission to do so;
  where that's missing the panel says so and disables those buttons rather than
  failing when they're pressed.
- The panel can find and add models, not just switch between saved ones. An
  "Add model" page searches HuggingFace, and picking a result measures it
  against this machine — weight size against the budget, model-card flags
  against the installed vLLM — before saving anything. Pulling saves a profile
  and switches to it, and saved models can be removed from the same list they
  are switched from. The judgements behind all of that now live in one place
  shared with the CLI, so the two cannot disagree about whether a model fits.
- `vllm-helper upgrade` replaces the binary from a published release, checking
  the download against the release's checksum before writing it. `--check`
  reports what's available without installing it.
- A dev channel. `upgrade --channel dev` follows a build of `main` that CI
  republishes on every commit that passes its tests, for running a fix before
  it's released; `--channel stable` goes back to tagged releases. The choice is
  remembered. Tagging a release is unchanged.
- `version` now also prints the commit the binary was built from, which is what
  distinguishes two dev builds: they share one moving release tag.
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

- The KV cache is sized for the model being served instead of being whatever
  vLLM finds left over. Every profile carried `GPU_MEMORY_UTILIZATION=0.85`
  regardless of what it was serving, and since that fraction is what decides the
  cache when nothing else does, a 1.1 GB model took 101.77 GiB of KV cache — 23
  full contexts of something nobody was asking 23 contexts of — on a 121 GiB
  machine that then sat at 112 GB used with 2 GB in swap. New profiles are
  written with `KV_CACHE_MEMORY_BYTES`, computed from the model's own attention
  shape and sized at `KV_CONTEXT_TARGET` full contexts (four by default, since
  nothing about the hardware can tell a single-user box from a shared endpoint),
  and `start` passes it as `--kv-cache-memory-bytes`. With the cache pinned,
  `--gpu-memory-utilization` becomes what its name always suggested: a ceiling
  on what vLLM may touch, not the size of the cache. Existing profiles are
  untouched and keep the old behaviour until they are re-created. Note that a
  profile written by this release names a flag older vLLM builds don't have.
- `inspect`, `search` and the panel report how much context a model can actually
  hold on this machine — tokens, the KV cache they cost, and how many times the
  model's own maximum that is. It's the question the size line was always
  standing in for.
- The model budget is measured from the machine rather than being a fixed
  percentage of it. The old rule handed over 70% of unified memory or 80% of
  VRAM; both numbers were invented, and on a 128 GB machine the first held back
  37 GiB for an operating system that vLLM's own profiler measures at under
  6 GiB. The budget now solves for the largest model whose weights, runtime
  overhead and a usable KV cache all fit, with the OS baseline read from the
  machine when it looks idle. Because it is a property of the hardware — one
  that changes when RAM is added — it is re-measured on every run instead of
  being frozen into `config.env` at install time. `MODEL_BUDGET_GB` is now an
  override, and `setup` writes it commented out. If you ran `setup` on an
  earlier version the number it wrote is still there, and now pins the budget to
  whatever that machine looked like then; comment it out to get the measurement.
  `doctor` says when an override is in force, so the figure it reports and the
  one `search` compares against can no longer disagree with no way to tell why.
- GGUF builds of mixture-of-experts models are marked unsupported rather than
  offered. vLLM's GGUF plugin maps fused expert weights for two architectures
  only (OLMoE and MiniMax-M2); every other MoE fails at load with one unmapped
  parameter per layer — after the service has restarted, so the model that was
  serving is already gone. The check reads the base model's `config.json`
  concurrently with the work already being done, so it costs no extra wall
  time, and the message names a safetensors quant as the way forward.
- Quants vLLM has no way to load are marked as such instead of being offered.
  It addresses a GGUF quant by name and only for a single file at a repo's
  root, so split-file and per-quant-subfolder copies are listed but not
  recommended, and a machine without the GGUF plugin is told so before a pull
  rather than after the restart. This also stops the recommendation landing on
  the least loadable entry: ranking on size alone picked bartowski's `bf16`,
  which is both split and in a subfolder.
- `inspect`, `search` and the panel now list every quantization a repo holds
  and lead with the biggest one that fits, instead of reporting only the
  smallest. The smallest is the right answer to "could anything here run" and
  the worst thing to actually serve — a 32B repo measured at 9.0 GB was being
  presented as a good fit for an 84 GB machine when that figure was its 2-bit
  quant and its 8-bit one was 34.8 GB. Quants too large for the machine are
  marked as such, and a repo whose smallest quant still doesn't fit now says
  so rather than advising you to look for a smaller one that isn't there.
  Pulling saves the chosen quant as `MODEL=repo:QUANT` wherever vLLM can be
  pointed at it, which means a single GGUF file at the repo root. Quants split
  across shards or filed under a per-quant subfolder are listed but cannot be
  selected, and the list says so rather than leaving you to find out.
- Coder sync is now a plugin (`internal/plugin/coder`) behind a small
  interface, rather than being wired through the CLI by hand. An integration
  declares its own configuration, contributes its own commands, and opts into
  the lifecycle events it cares about; anything it doesn't ask for is never
  delivered, so an integration nobody uses can't slow down a switch or break
  one when it's down. Nothing changes for users: `coder-sync` takes the same
  verbs, reads the same `CODER_URL`/`CODER_API_TOKEN`/`CODER_AI_PROVIDER_ID`,
  and keeps its state in the same file. The motivation is that Coder's
  model-config endpoint is an undocumented experimental API that has already
  changed shape once, and this keeps that churn in one package.
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
- The boot log banner says plainly that Ctrl+C stops watching and leaves the
  server loading, points at `status` for checking back, and mentions
  `--no-follow`. On a first run the model download keeps that log on screen for
  a long time, and people were interrupting it believing they had killed the
  server.
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

- gpt-oss models are set up so they can actually be used. Their cards carry no
  `vllm serve` snippet, so nothing was detected and the profile was written with
  no flags at all. Served that way the model answers ordinary chat normally but
  returns an empty message for anything involving tools, which reads as the
  model ignoring the request rather than as a missing flag. `new` now adds the
  parsers that read this format, and fetches the vocab files the tokenizer
  reads from disk, both only where the installed vLLM confirms them.
- Installing the GGUF plugin from the panel no longer holds a request open for
  the whole build. It builds from source, so that request stayed open for
  minutes and browsers drop those — after which the page could not tell a
  finished install from a lost one, and the model card only showed the truth
  if you searched for it again. The install now runs on the server and the
  page polls it, which also means a reload rejoins one already in progress
  rather than offering to start a second.
- The capability probe had stopped working wherever a vLLM plugin was
  installed. vLLM logs plugin registration to stdout, so the probe's JSON was
  no longer the only thing on the stream and parsing it yielded nothing —
  which reads as "we could not ask", so model-card flag validation quietly
  stopped running, nothing was cached, and an installed GGUF plugin was
  reported as missing.
- `inspect` and the panel's model detail were running `vllm --version` on
  every call, before consulting the cache, and that imports vLLM. Measured at
  10.1s per lookup on a DGX Spark against 0.4s of real work; now 0.25s once
  warm, by reading the version from distribution metadata instead.
- `new` no longer writes a context length the model can't support when its
  README has no `vllm serve` snippet. The clamp to the model's real
  `max_position_embeddings` sat behind an early return taken on exactly those
  cards, so a model like `facebook/opt-125m` (2048) got the generic 131072
  default and vLLM refused to start. Cards with no snippet are the ones that
  need the clamp most.
- `--gpu-memory-utilization` is clamped to what's actually free on Linux hosts
  with no NVIDIA GPU. There the flag reserves a fraction of _system_ RAM rather
  than VRAM, so the 0.85 default asked for 85% of the machine and aborted
  startup. Hosts with a discrete card are untouched, and macOS already did this.
- `--gpu-memory-utilization` is capped on machines where the GPU shares one
  memory pool with the host — GB10, Jetson, Thor. The old guard asked "is there
  an NVIDIA GPU?", which is the right question for discrete VRAM, where nobody
  else can take it, and the wrong one for unified memory, where the flag is a
  slice of the whole machine and the remainder is all the OS and page cache get.
  Overshooting there is worse than elsewhere: the allocation failure lands below
  the CUDA runtime, so instead of a clean OOM the host can wedge hard enough to
  need a power cycle. Such machines take a 0.85 cap rather than a live
  measurement — measuring free memory during a restart reads the outgoing
  server, which on a DGX Spark serving a 68 GB model turned 0.85 into 0.06 and
  left a cache too small to run. Discrete cards and the CPU backend are
  unchanged.
- A machine with no NVIDIA GPU is no longer charged for a CUDA context and graph
  capture it never creates. Those reserves were all measured on a GPU box, so a
  7 GB CPU-only host came out with a budget of zero and was told nothing would
  run, when vLLM's CPU backend serves small models there quite happily. A
  machine that genuinely cannot serve anything now says so, rather than
  reporting a budget that happens to be zero.
- Fit checks were 7.4% pessimistic. HuggingFace reports sizes in decimal GB and
  memory was being read in GiB, so models that did fit were called too big.
  Sizes are carried in bytes internally now.
- A model's weights are measured wherever they live. The listing only looked at
  the repo root, so a model keeping its weights one level down measured as 0 and
  was reported as "unknown" rather than as a size, and a sharded repo whose
  listing ran past one page was never counted in full. Listing is recursive and
  follows the Hub's pagination now, running out of pages is an error rather than
  a short answer, and a measurement that fails says it could not be measured
  instead of being rendered as a model with no servable weights — the two read
  as opposites and only one of them is a reason not to pull.
- Coder sync follows the model-config collection to its new route. Coder moved
  it from `/api/experimental/chats/model-configs` to
  `/api/v2/organizations/{org}/chats/models` and reserves the old path, so
  against a 2.37 deployment every sync failed with an HTTP 404 that read like a
  bad `CODER_URL` or an expired token — which is exactly what `doctor` said. The
  client now asks the deployment which collection it serves, once per command,
  and both generations stay supported because both are in the wild. A saved
  entry ID that has outlived its entry — deleted in Coder's UI, or left by
  another deployment — is also told apart from a missing collection now:
  `activate` re-creates it rather than giving up.
- `status` distinguishes a service that is failing to start from one that is
  still loading. Both leave the endpoint unresponsive, and reporting a
  crash-looping unit as "still loading" sent people off to wait for a server
  that was never coming up. `status` and `doctor` now read the init system's
  restart counter and sub-state, and `status` shows the last log line, which is
  usually the exception that killed it.
- Flags taken from a model card are checked against the vLLM installed here
  before they reach a profile. A card is written for whatever vLLM its author
  ran, and anything it asks for that this build won't accept — a parser that
  isn't registered, a flag that has since been removed — used to be copied
  verbatim and only surface as a crash loop on the next switch. NVIDIA's
  Nemotron-3-Nano is the case that prompted this: its card prescribes a parser
  supplied as a plugin file, which ships with the model rather than with your
  machine. Such flags are now dropped with an explanation, and when the
  installed build has a plausible alternative the message names it. Nothing is
  substituted automatically: parsers one character apart are different parsers,
  and a wrong guess changes reasoning output silently. `new` and `inspect`
  clean before writing; `switch` and `restart` only warn, since a profile
  someone explicitly asked for is theirs to serve.
- More of a model card's `vllm serve` flags are adopted. Only five were
  recognised, so anything else — including memory-shaping settings like
  `--max-num-seqs` — was dropped without a word, and the served configuration
  quietly differed from what the model's authors prescribed. Flags vllm-helper
  sets itself, flags describing someone else's GPU topology, and anything that
  looks like a credential are still left out.
- `set` and `edit` refuse a `MAX_MODEL_LEN` that isn't a positive number
  instead of writing it. It reached vLLM's command line verbatim and killed
  the server on startup, and the only check for it was on the Coder sync path,
  so a machine without Coder configured got no warning at all.
- `upgrade --check --channel <c>` no longer reports a difference when the
  running build is already what that channel points at. The up-to-date check
  was skipped whenever a channel was named, so checking the channel you were
  already on always claimed an update was available.
- `upgrade --check` no longer writes `UPDATE_CHANNEL` to `config.env`. Passing
  `--channel` to a command documented as reporting "without installing it"
  quietly changed the stored preference.
- `--version` prints the commit, matching `version`. The commit is what tells
  two dev builds apart, so the flag was the less useful of the two spellings.
- A profile whose `MAX_MODEL_LEN` isn't a positive number is reported before
  it reaches Coder. It was parsed with the error discarded, so a bad value
  became a context limit of 0, which Coder rejects with an HTTP 400 that says
  nothing about which local setting was at fault.
- Redirecting output to `/dev/null` no longer looks like a terminal. Terminal
  detection read the file mode, and `/dev/null` is a character device like a
  terminal is, so `vllm-helper logs > /dev/null` followed the log forever
  instead of printing it and exiting, and `sudo` was left prompting with
  nothing able to answer. The check now asks the kernel.
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

[Unreleased]: https://github.com/coder/vllm-helper/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/coder/vllm-helper/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/coder/vllm-helper/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/coder/vllm-helper/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/coder/vllm-helper/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/coder/vllm-helper/releases/tag/v0.1.0
