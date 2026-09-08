package spark

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/plugin"
)

func init() { plugin.Register(Plugin{}) }

type Plugin struct{}

func (Plugin) Name() string                                             { return "spark" }
func (Plugin) Configured(_ *config.Config) bool                         { return true }
func (Plugin) Events() []plugin.Event                                   { return nil }
func (Plugin) Handle(context.Context, plugin.Event, plugin.State) error { return nil }
func (Plugin) Check(context.Context, *config.Config) error              { return nil }

func (Plugin) Commands() []*cobra.Command {
	c := &cobra.Command{Use: "spark", Short: "DGX Spark helpers", RunE: func(*cobra.Command, []string) error { return errors.New("not implemented") }}
	return []*cobra.Command{c}
}
