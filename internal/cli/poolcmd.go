package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newPoolCmd() *cobra.Command {
	c := &cobra.Command{Use: "pool", Short: "Replica pools (router)", GroupID: groupMachine}
	c.AddCommand(newPoolNewCmd(), newPoolHTTPStatusCmd(), newPoolServeCmd(), newPoolInstallServiceCmd())
	return c
}

func newPoolNewCmd() *cobra.Command {
	var nodesCSV, bind, sticky string
	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Create a pool (scaffold)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if nodesCSV == "" {
				return errors.New("--nodes is required")
			}
			fmt.Printf("Pool %s: nodes=%s bind=%s sticky=%s (not implemented)\n", args[0], nodesCSV, bind, sticky)
			return nil
		},
	}
	cmd.Flags().StringVar(&nodesCSV, "nodes", "", "Comma-separated node names")
	cmd.Flags().StringVar(&bind, "bind", ":8001", "Bind address for router")
	cmd.Flags().StringVar(&sticky, "sticky", "auto", "Stickiness (auto|authorization|header:Name)")
	return cmd
}
