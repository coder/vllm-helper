package cli

import (
	"github.com/spf13/cobra"
)

// A pool is not a stored object: "pool serve" takes its members on the command
// line and "pool install-service" records them in the unit it writes. There was
// a "pool new" here that validated --nodes and printed "(not implemented)";
// a command that advertises itself in --help and then does nothing is worse
// than one that is absent, so it is gone until a pool is something to create.
func newPoolCmd() *cobra.Command {
	c := &cobra.Command{Use: "pool", Short: "Replica pools (router)", GroupID: groupMachine}
	c.AddCommand(newPoolHTTPStatusCmd(), newPoolServeCmd(), newPoolInstallServiceCmd())
	return c
}
