---
title: Introduction
description: What vllm-helper is, and when to reach for it.
---

vllm-helper runs one large model at a time on a single GPU machine, and switches models with one command.
It wraps [vLLM](https://github.com/vllm-project/vllm)'s OpenAI-compatible server with saved model profiles, HuggingFace search, and an optional [Coder](https://coder.com) Agents sync.

It was built for unified-memory machines like the NVIDIA DGX Spark, where memory fits exactly one big model.
It works on any Linux machine with an NVIDIA GPU, and experimentally on macOS through vLLM's CPU backend.

## How it fits together

- **A profile** is a small env file holding the `vllm serve` settings that work for one model.
- **A service unit** runs the active profile: a systemd unit on Linux, a launchd job on macOS.
- **The CLI** switches profiles, pulls models, and diagnoses the machine.
- **The web panel** is what `vllm-helper serve` serves: the same controls in a browser.
- **The endpoint** is vLLM's own OpenAI-compatible API, on port 8001 by default.

Inference traffic goes straight to vLLM on its own port.
vllm-helper is a control plane, so nothing here sits in the path of a request — a bug in it cannot break inference.

The binary is self-contained: one file to put on a machine, with the docs you are reading compiled in.

## Next steps

- Follow the [Quickstart](./quickstart.md) to set the machine up and serve a first model.
- See what a profile holds in [Profiles](./profiles.md).
- Serve a model measured across several machines with [Recipes](./recipes.md).
- Browse the [CLI reference](./reference/cli.md) for every command.
