# vllm-helper

A small CLI for running a **single-model, OpenAI-compatible vLLM server** on
one machine and switching which model it serves -- built for boxes like the
NVIDIA DGX Spark / Grace-Blackwell systems (128GB unified memory) where only
one large model fits at a time, but useful on any Linux machine with a GPU.

What it gives you over raw `vllm serve`:

- **Saved model profiles** -- each model's flags (`--tool-call-parser`,
  `--reasoning-parser`, context length, ...) live in a small env file you
  write once and switch to by name.
- **One-command switching** -- `vllm-helper switch <profile>` rewrites the
  runtime config, restarts the systemd service, and tails the boot log.
- **HuggingFace search built in** -- `vllm-helper search <query>` opens an
  fzf picker with live size/license/quantization info and a "will it fit on
  this machine" check, then pulls and switches to whatever you pick.
- **Flag auto-detection** -- `vllm-helper new <org/model>` reads the model's
  HF card and pre-fills known `vllm serve` flags into the new profile.
- **Optional [Coder](https://coder.com) Agents sync** -- if configured, every
  switch keeps a Coder deployment's AI-model picker in sync so users can only
  select the model that's actually loaded.

## Requirements

- Linux with systemd (macOS support is planned; see Roadmap)
- An existing vLLM install (typically in a venv -- the installer finds
  `vllm` on your PATH, or you set `VLLM_BIN` in config)
- `bash`, `curl`, `python3`; `fzf` for the interactive search (optional)

## Install

```bash
git clone https://github.com/coder/vllm-helper && cd vllm-helper
./install.sh          # system-scope systemd unit (needs sudo)
# or
./install.sh --user   # user-scope unit, no sudo (enables lingering)
```

The installer is idempotent (safe to re-run after `git pull`), never
overwrites your config or profiles, and never restarts a running server. It
creates:

| Path | What |
|---|---|
| `~/.config/vllm-helper/config.env` | Global settings: port, `VLLM_BIN`, model-size budget, optional Coder settings |
| `~/.config/vllm-helper/secrets.env` | Generated `VLLM_API_KEY` (and Coder token if used) -- chmod 600 |
| `~/.config/vllm-helper/models/*.env` | Your model profiles (seeded with curated examples) |
| `~/.local/state/vllm-helper/` | Machine state: the generated runtime env, active profile, Coder entry IDs |
| `~/.local/share/vllm-helper/` | Downloaded data (tiktoken vocab files for gpt-oss-style models) |

## Usage

```bash
vllm-helper status                  # is it running, what model, is it healthy
vllm-helper list                    # saved profiles
vllm-helper switch qwen3-coder-next # switch to a saved profile
vllm-helper new <org/model-repo>    # pull, profile, and switch to a new HF model
vllm-helper search <query>          # interactive HuggingFace search (needs a real terminal)
vllm-helper remove <profile>        # delete a profile (refuses if active)
```

Point any OpenAI-compatible client at `http://<machine>:8001/v1` with the API
key from `secrets.env`. The model name to request is whatever `vllm-helper
status` shows as active.

Switching restarts the service and reloads the model -- a few minutes if the
weights are cached on disk, much longer the first time (however big the
download is). `switch`/`new`/`search` tail the boot log automatically; Ctrl+C
stops watching without stopping the server.

## Profiles

A profile is a small env file in `~/.config/vllm-helper/models/`:

```bash
# Status: TESTED AND WORKING (verified 2026-07-29)
MODEL=Qwen/Qwen3-Coder-Next-FP8
MAX_MODEL_LEN=131072
GPU_MEMORY_UTILIZATION=0.85
EXTRA_ARGS="--enable-auto-tool-choice --tool-call-parser qwen3_coder"
```

The repo ships curated examples (see `profiles/`) that were verified on
128GB Grace-Blackwell hardware, including working flag sets for
Qwen3-Coder-Next, gpt-oss-120b, and others -- each file's comment header
says what's known about it.

**Gotcha:** if `EXTRA_ARGS` has more than one flag it **must** be quoted
(`EXTRA_ARGS="--foo --bar baz"`). Unquoted, bash treats everything after the
first space as a command to run and the service fails with a confusing
`command not found`.

### Adding a brand-new model

1. **Check it fits first.** Rule of thumb: `total_params x bytes_per_param`
   must stay under the `MODEL_BUDGET_GB` in your config (the installer
   defaults it to ~70% of total memory). Bytes per param: FP8/INT8 ≈ 1,
   INT4/AWQ/GPTQ-4bit ≈ 0.5, native MXFP4 ≈ 0.5-0.6. Prefer the smallest
   *official* quantized release over raw BF16 weights.
2. `vllm-helper search <query>` (interactive, shows a live size check) or
   `vllm-helper new <org/model-repo>` if you already know the repo ID.
   Both auto-detect known `vllm serve` flags from the model card when the
   card includes a snippet -- the profile's `# Status:` line says whether
   that worked. If not, read the card and edit `EXTRA_ARGS` by hand.
3. **Verify before trusting it:** `curl localhost:8001/health` returns 200, a
   real chat completion returns populated `content`, and (if you need tools) a
   request with a `tools` array actually surfaces `message.tool_calls` --
   some models (gpt-oss) only do tool calls over `/v1/responses`.

## Coder Agents sync (optional)

If you register the machine as an `openai-compat` AI provider in a Coder
deployment, `vllm-helper` can keep Coder's model picker truthful: every
profile gets one persistent model-config entry, and only the currently
active profile is `enabled`/`is_default` -- so users literally cannot select
a model that isn't loaded. Disabled entirely unless configured.

Set in `config.env`:

```bash
CODER_URL=https://coder.example.com
CODER_AI_PROVIDER_ID=<uuid of your openai-compat provider>
```

and `CODER_API_TOKEN` in `secrets.env`. A sync failure never blocks a local
switch -- the scripts warn and continue. `vllm-helper coder-sync
<activate|register|remove> <profile>` is the manual escape hatch.

## Troubleshooting

- **Service won't start / crash loop:** `journalctl -u vllm.service -n 100
  --no-pager` (add `--user` for user scope) -- read the actual Python
  traceback, don't guess.
- **Health endpoint not responding:** probably still loading weights (5-10+
  min for a big model). Watch `journalctl -u vllm.service -f -o cat`.
- **Permission denied writing the HF cache:** something ran as root and wrote
  into `~/.cache/huggingface`. Fix ownership: `sudo chown -R $USER
  ~/.cache/huggingface`.
- **"Model architectures [...] failed to be inspected" on startup:** missing
  build tooling for a Triton kernel compile step -- install your distro's
  `python3-dev` package.
- **Empty `content` with only `reasoning` populated:** the model uses a
  structured output format that isn't parsed for chat-completions. Try
  `/v1/responses`, or check the model card for a `--reasoning-parser` flag.
- **After a reboot:** the service is enabled and comes back on its own (the
  model reloads from disk cache). If not: `systemctl status vllm.service`.

## How it's wired up

```
vllm-helper              CLI entry point (symlinked onto PATH by install.sh)
install.sh               Idempotent installer -- see Install above
vllm.service.template    Rendered into the systemd unit by install.sh
profiles/                Curated example profiles, copied to your config on install
lib/
  common.sh              Shared bootstrap: XDG dir resolution, config loading
  start.sh               What systemd runs -- sources the generated runtime env, execs vllm serve
  switch.sh              Core switch/new/remove/list logic
  search.sh              fzf HuggingFace search picker
  preview.sh             fzf preview pane (size/license/fit check)
  detect-flags.sh        Auto-detects vllm serve flags from a model's HF card
  sync-coder.sh          Optional Coder Agents sync
```

Nothing ever writes into the checkout -- config, secrets, state, and data all
live in the XDG paths listed under Install.

## Roadmap

This is the bash-based v0. Planned next (tracked in issues):

- A Go rewrite shipping as a **single static binary** for linux/amd64,
  linux/arm64, and macOS, with first-run **detection and adoption of existing
  vLLM setups** (host processes, systemd units, Docker/Podman containers).
- Hardware-aware sizing (unified memory vs discrete VRAM) instead of a
  static budget.
- macOS remote-control mode for managing a Linux vLLM box.

## License

[Apache-2.0](LICENSE)
