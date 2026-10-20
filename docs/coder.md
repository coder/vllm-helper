---
title: Coder integration
description: Keep a Coder deployment's model picker truthful.
---

If this machine backs an `openai-compat` AI provider in a [Coder](https://coder.com) deployment, vllm-helper can keep the deployment's model picker truthful: every profile gets a model-config entry, and only the loaded model is enabled.

## Configure it

1. Set `CODER_URL` and `CODER_AI_PROVIDER_ID` in `~/.config/vllm-helper/config.env`.
2. Set `CODER_API_TOKEN` in `~/.config/vllm-helper/secrets.env`.
3. On a deployment with more than one organization, set `CODER_ORGANIZATION` in `config.env` to yours. It defaults to `default`.

Sync failures never block a local switch: a deployment that cannot be reached is reported, and the model loads anyway.

## What syncs when

Every switch updates the deployment.
Removing a profile removes its entry.

`vllm-helper coder-sync` also takes verbs of its own:

```sh
vllm-helper coder-sync register <profile>   # (re-)create the entry for one profile
vllm-helper coder-sync register-all         # backfill entries for every profile
vllm-helper coder-sync prune                # entries no local profile accounts for
```

- `register-all` adds a disabled entry for any profile that does not have one yet; entries that exist are left as they are.
- `prune` reports entries this provider offers that no local profile can serve, and deletes them with `--yes`.
  An entry naming a model this machine cannot serve is one the picker offers and the server then refuses, so they are worth clearing.

`vllm-helper doctor` verifies the configured URL and token against the API.

## One note on stopping

Stopping the server with `vllm-helper stop` does not disable the active model in Coder's picker — it stays enabled until the next switch.

## Next steps

- [Profiles](./profiles.md) — what gets an entry.
- [CLI reference](./reference/cli.md) — every verb and flag.
