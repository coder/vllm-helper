---
title: Providers
description: The inference servers a profile can run on, and what each one changes.
---

A provider is the inference server a profile runs on.
llm-helper does the same work for every provider: profiles, switching, the service unit, the health check, and Coder sync.
What changes is the command the service runs, and which of llm-helper's checks apply.

| Provider | Server                         | Runs on                             | `PROVIDER` |
| -------- | ------------------------------ | ----------------------------------- | ---------- |
| vLLM     | `vllm serve`                   | Linux with an NVIDIA GPU, and macOS | `vllm`     |
| MLX      | `mlx_lm.server`, from `mlx-lm` | Apple Silicon                       | `mlx`      |

vLLM is the default, and it is what every install from before providers existed runs.

## How a profile picks one

Each profile records its own provider in a `PROVIDER=` line.
A profile without one is a vLLM profile.

`PROVIDER` in `config.env` only picks the provider for the profiles that `new`, `search` and the panel create from then on.
Changing it leaves your existing profiles on the server they were made for, so a machine can hold vLLM and MLX profiles side by side and switch between them.

## MLX

[mlx-lm](https://github.com/ml-explore/mlx-lm) serves models on the Apple Silicon GPU through MLX.
It reads a model's tool-call and reasoning formats from its tokenizer, so an MLX profile needs no parser flags.
Use a repo already converted to MLX, such as one from [mlx-community](https://huggingface.co/mlx-community), for quantized weights.

1. Install mlx-lm, usually in a venv:

   ```sh
   uv venv ~/venvs/mlx
   VIRTUAL_ENV=~/venvs/mlx uv pip install mlx-lm
   ```

2. On a new machine, run setup for MLX. On a machine that is already set up, add the two lines to `~/.config/vllm-helper/config.env` instead:

   ```sh
   llm-helper setup --provider mlx
   ```

   ```sh
   PROVIDER=mlx
   MLX_LM_BIN=/Users/you/venvs/mlx/bin/mlx_lm.server
   ```

3. Pull a model:

   ```sh
   llm-helper new mlx-community/Qwen3-Coder-30B-A3B-Instruct-8bit
   ```

An MLX profile holds only `PROVIDER`, `MODEL` and `EXTRA_ARGS`.
The service runs `mlx_lm.server --model <MODEL> --port <PORT>` followed by `EXTRA_ARGS`.
vLLM's settings do not apply, so llm-helper does not copy flags from the model card, size a KV cache, or fit a memory fraction for it.

Things to know about `mlx_lm.server`:

- **It checks no API key.** `VLLM_API_KEY` does nothing for an MLX profile, and any client that can reach the port can use it.
- **It listens on `127.0.0.1` by default.** To serve another machine, add `--host 0.0.0.0` to `EXTRA_ARGS`, and only do that on a network you trust.
- **A request can load another model.** The server loads whatever repo a request names in `model`, so an open port can be made to download and load any model.
- **Replies stop at 512 tokens** when a client sends no `max_tokens`. Add `--max-tokens <n>` to `EXTRA_ARGS` to raise that default.
- **Its health check answers while the model is still loading.** `status` and `switch --wait` can report the endpoint up before the first request can be served.

```sh
llm-helper set <profile> -- EXTRA_ARGS='--host 0.0.0.0 --max-tokens 32768'
```

## What only vLLM does

These apply to vLLM profiles alone:

- Reading flags from the model card, and checking parsers against the installed vLLM.
- KV cache sizing and the `GPU_MEMORY_UTILIZATION` fit.
- GGUF quants: `new --quant` refuses when new profiles go to another provider.
- `adopt`, which looks for running vLLM servers, and recipes, which run vLLM containers.
- `shard`, which refuses a profile for another provider.
