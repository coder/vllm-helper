// vllm-helper manages a single-model, OpenAI-compatible vLLM server:
// saved model profiles, one-command switching, HuggingFace search, and
// optional Coder Agents sync. See README.md.
package main

import (
	"os"

	"github.com/coder/vllm-helper/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
