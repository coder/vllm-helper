# Recipes as the golden path

## Goal

Running a model should start from a curated recipe we have actually run, not from a
HuggingFace search plus autodetection. Search stays, clearly labelled as the escape
hatch for models we have no recipe for.

## Source authority

Two upstreams, each authoritative for a different thing. Mixing this up is how we
shipped a flag the image rejects (`--attention-backend B12X_MLA_SPARSE`).

| source                   | authoritative for                                                                           | licence    |
| ------------------------ | ------------------------------------------------------------------------------------------- | ---------- |
| `vllm-project/recipes`   | model facts: repo ids, variants, `min_vllm_version`, the `hardware:` verified map           | Apache-2.0 |
| `eugr/spark-vllm-docker` | image facts: backend names, loader, `mods`, `build_args`, anything describing its own image | MIT        |

eugr is the higher-signal source for DGX Spark / GB10 because its recipes are written
for that hardware and shipped with the image they target. vllm-project is broader but
hardware-blind for SM121.

eugr is MIT (Copyright 2026 Eugene Rakhmatulin); derived data must carry attribution.

## Three roles, kept separate

Conflating these is what made ingestion look hard.

1. **Launch path — our tested recipes only.** Digest-pinned, measured on our own
   hardware, `internal/recipe/tested.json`. This is the product.
2. **Search path — HF search, annotated.** "eugr has a recipe for this", "vllm-project
   claims gb200-verified". Signal without a claim that it runs here.
3. **Dev path — a diff tool, not an importer.** Parses both upstreams and reports where
   our tested set disagrees. Dev-only: it never ships in the binary and cannot break a
   launch.

The critical consequence: **we never execute eugr's `command:` template, only compare
against it.** Comparison tolerates a partial parse; execution does not. That removes
every integration risk below.

### Why not ingest eugr directly

- `command:` is a templated shell string, not an arg list: line continuations,
  `{placeholder}` substitution, `{{...}}` escaping literal JSON.
- `container`, `build_args` and `mods` reference eugr's build pipeline. We consume the
  published image by digest, so for us those fields are advisory.
- The repo moves fast. Anything mechanically ingested becomes a moving dependency.

### Why not drop the upstreams entirely

Without a systematic diff there is no way to learn we have fallen behind. That is not
hypothetical: we ran DeepSeek for a week with no speculative decoding because the
recipe was transcribed from a log instead of from eugr's YAML.

## Upstream schemas

eugr, `recipes/*.yaml`, `recipe_version: "1"` (uniform across all recipes, documented in
their `docs/AGENT_DEVELOPMENT.md`, and used by `run-recipe.py` for feature checks):
`model` (HF repo, the join key), `container`, `build_args[]`, `cluster_only`, `mods[]`,
`defaults{...}`, `env{}`, `command: |`.

vllm-project, `models/<org>/<Model>.yaml`: `model.model_id` (same join key),
`docker_image`, `min_vllm_version`, `base_args`, `meta.hardware`, `variants`,
`compatible_strategies`. Only entries listed under `hardware:` are verified; everything
else is a fail-open default, and we surface that distinction rather than inventing tiers.

## Phases

- **P1 — done.** Tested-recipe schema, DeepSeek entry, `recipes list` / `recipes show`.
- **P2 — DeepSeek deltas.** Resolve the seven divergences below, re-verify on the two
  Sparks, re-stamp. A tested entry that disagrees with reality is worse than none.
- **P3 — diff tool.** `tools/recipediff` with its own `go.mod` so the CLI keeps its two
  direct deps. Parses both upstreams at pinned commits, emits a conflict report. Reports
  disagreement; never silently resolves it.
- **P4 — instantiate.** `new --recipe <id>` creates a local profile. Fit math changes
  role: it validates the recipe's declared topology against the actual machine instead
  of deciding topology. Backend names validated against the image before launch.
- **P5 — launch.** Spark hardware profile discovering `IFACE_NAME`, `IB_IF`, `HEAD_IP`
  with placeholder expansion; native Go two-node launcher that restores profile, router
  and Coder sync (the raw `docker run` path bypassed all three).
- **P6 — maintain.** `recipe verify` re-stamps from a live run. Relabel search tiers.

## Recipe #1: DeepSeek V4 Flash

`deepseek-v4-flash-2spark`, 2 nodes sharded TP=2, image pinned by digest. Verified at
262144 context: 675,260 KV tokens, 2.58x concurrency, ~150 s load.

Open deltas against `eugr/spark-vllm-docker@1761c49a`, to settle in P2:

| field                            | eugr                                     | ours        | status                            |
| -------------------------------- | ---------------------------------------- | ----------- | --------------------------------- |
| `--speculative-config`           | `dspark`, 5 tokens, probabilistic        | absent      | test; likely a throughput win     |
| `mods`                           | `instanttensor-hybrid-draft-loader`      | not applied | dspark draft loader depends on it |
| `--reasoning-config`             | explicit parser, empty start/end         | absent      | test                              |
| `--default-chat-template-kwargs` | `thinking=true`, `reasoning_effort=high` | absent      | test                              |
| `VLLM_USE_MEGA_AOT_ARTIFACT`     | `1`                                      | `-1`        | ours, deliberate                  |
| `VLLM_MOE_SKIP_PADDING`          | `0`                                      | unset       | test                              |
| `max_cudagraph_capture_size`     | 48                                       | 64          | test                              |

Deliberate divergences to keep: port 8001 (matches `PORT`, fronted by the router on 8011) and an explicit `--max-model-len` rather than `auto`, so the served window and
`verified.context` stay pinned together.

## Invariants

- Images pinned by `@sha256:`, never a moving tag. eugr's `:latest` advanced under us
  mid-session, and a moved image is how a valid backend name becomes invalid.
- Tested means tested: strict list, no aspirational entries.
- Separate ids per topology rather than one entry with a topology switch.
- `--max-model-len` must equal `verified.context`, enforced by
  `TestVerifiedContextMatchesMaxModelLen`.
- Extend upstream, do not fork it.
