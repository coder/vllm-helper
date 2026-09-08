package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newShardCmd() *cobra.Command {
	c := &cobra.Command{Use: "shard", Short: "Sharded (distributed) serving"}
	c.AddCommand(&cobra.Command{Use: "new <name>", Args: cobra.ExactArgs(1), Short: "Create sharded config", Run: func(_ *cobra.Command, args []string) { fmt.Println("not implemented") }})
	c.AddCommand(&cobra.Command{Use: "status <name>", Args: cobra.ExactArgs(1), Short: "Show sharded status", Run: func(_ *cobra.Command, args []string) { fmt.Println("not implemented") }})
	return c
}
