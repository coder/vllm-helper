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

## Provenance

Every tested recipe is authored in this repo but derived from an upstream, so each entry
carries a structured `source`:

```json
"source": {
  "repo": "eugr/spark-vllm-docker",
  "path": "recipes/deepseek-v4-flash-0731.yaml",
  "commit": "1761c49ad71ca9c8ca50772941ee4d06dcc40dc1",
  "license": "MIT"
}
```

Structured rather than a comment for three reasons: JSON has no comments; `recipediff`
needs it to know which upstream file and commit to compare an entry against; and MIT and
Apache-2.0 both want attribution. `recipes show` renders it as a "derived from" line, and
`TestTestedRecipesCiteAPinnedSource` rejects an entry without a repo, a full 40-character
commit and a licence.

The pinned commit is also the staleness signal: when it trails upstream HEAD,
`recipediff` has something to report.

### Declared deviations

Where an entry departs from its source, `source.deviations` records it as data:

```json
{
  "setting": "max_num_seqs",
  "upstream": "8",
  "ours": "32",
  "why": "measured ..."
}
```

An empty `ours` means we dropped the setting. `setting` must name the thing the way
upstream does — the flag without its dashes (`max_model_len`), the env key, or the mod
path — because that is the key `recipediff` matches on, and
`TestDeviationsMatchTheArgsTheyDescribe` checks the claim against the entry's own args.
Prose in `notes` cannot do either job.

Running the tool against both entries found eight differences that were never decisions,
only transcription. Four per entry remain open and are deliberately left undeclared so the
report keeps raising them: `--max-cudagraph-capture-size` 48 to 64,
`--reasoning-config` dropped, both `--default-chat-template-kwargs` dropped,
`VLLM_USE_MEGA_AOT_ARTIFACT` 1 to -1, and `VLLM_MOE_SKIP_PADDING` dropped. Each needs a
measurement or a reason, not a sentence invented after the fact.

## Local overrides

A tested recipe is the **base layer**; the profile of the same name is the **override
layer**. `recipes launch` merges them last-wins per flag, which is the precedence vLLM
itself documents for a command line over a config file.

Editing is allowed and honoured. The earlier design refused edits to a recipe profile, but
the profile is not inert: the Coder sync reads `MAX_MODEL_LEN` and `DISPLAY_NAME` from it, while
the launcher built argv from the recipe alone. An edit therefore did half of something — it
moved what Coder advertised without moving what the server served. Merging removes that
asymmetry by construction.

What changes when overrides exist is the **claim**, not the permission:

- `recipes list`, `recipes show`, `list` and the launch output report the entry as the recipe
  plus local overrides, and `recipes show` stops presenting the verified numbers as if they
  described the running server.
- A future `recipe verify` must refuse to re-stamp measurements taken from an overridden
  run, because "we ran this" would then be false.

What is still refused is a **wrong command**, not an edit: native `start` and `switch` on a
container recipe. `switch` is refused ahead of `WriteActiveEnv` because activation notifies
Coder before restarting, so a late failure would leave Coder advertising a model that is
not serving.

Overriding any flag is allowed, including `--attention-backend` and `--load-format`. The
`forbiddenArgs` check still fails closed on combinations known to abort on this arch. A
profile that merely restates a recipe value is not an override, so a freshly generated
profile reports none. Changing `MODEL` is refused: that is a different model, which the
recipe's flags and measurements do not describe.

### Worked example: speculative decoding is a single-user override

`dspark` speculative decoding is the clearest case for the override layer rather than the
recipe. Measured on the two-Spark pair at 262K context, single stream:

| workload              | recipe     | plus dspark |
| --------------------- | ---------- | ----------- |
| repetitive/structured | 28.8 tok/s | 80 tok/s    |
| code                  | 28.8 tok/s | 44-56 tok/s |
| prose                 | 28.8 tok/s | 40 tok/s    |

It is not in the recipe because the cost lands on everyone else: KV cache falls from
681,894 to 420,192 tokens, concurrency from 2.60x to 1.60x, and load time from 150s to
195s. Speculation also spends spare compute between tokens, and on a busy server that
compute is already carrying other requests, so the single-stream numbers above are an
upper bound rather than what a shared box delivers. Time-to-first-token gets slightly
worse, which every waiting request pays for.

Enable it per-deployment when the box has one user:

```
vllm-helper set deepseek-v4-flash-2spark EXTRA_ARGS='--speculative-config {"method":"dspark","num_speculative_tokens":5,"draft_sample_method":"probabilistic","attention_backend":"B12X","draft_load_config":{"load_format":"safetensors","safetensors_load_strategy":"lazy"}} --max-cudagraph-capture-size 48'
vllm-helper recipes launch deepseek-v4-flash-2spark
```

`num_speculative_tokens` must be a multiple of the checkpoint's `dspark_block_size` (5 for
DeepSeek V4 Flash 0731); 3 truncates the block and 6 is rejected at boot. The capture size
is `max_num_seqs x (k + 1)`. Confirm acceptance from the server's own `SpecDecoding
metrics` log lines rather than from client-side tokens per second, because under
speculation one streamed chunk carries several tokens.

## Phases

- **P1 — done.** Tested-recipe schema, DeepSeek entry, `recipes list` / `recipes show`.
- **P2 — DeepSeek deltas.** Resolve the seven divergences below, re-verify on the two
  Sparks, re-stamp. A tested entry that disagrees with reality is worse than none.
- **P3 — diff tool. Done for eugr.** `tools/recipediff` with its own `go.mod` so the CLI
  keeps its two direct deps. `go run . -upstream-dir testdata` compares offline against
  checked-in fixtures; with no flag it fetches each entry's source at its pinned commit and
  also reports when upstream has moved past that pin. Exits non-zero while anything is
  undeclared. The vllm-project side is not implemented: neither shipped entry cites it, and
  it needs a model-id to file mapping the `source` block does not carry.
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
