package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/nodes"
)

func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "node",
		Short:   "Register machines by HTTP URL and SSH target",
		Long:    "Register machines by HTTP URL (--host) and SSH target (--ssh).\nRecipes, pools and sharding name them with --nodes.",
		GroupID: groupMultiNode,
	}
	cmd.AddCommand(newNodeAddCmd(), newNodeListCmd(), newNodeRemoveCmd())
	return cmd
}

func newNodeAddCmd() *cobra.Command {
	var host, sshFlag string
	c := &cobra.Command{Use: "add <name>", Args: cobra.ExactArgs(1), Short: "Add a node",
		RunE: func(_ *cobra.Command, args []string) error {
			if host == "" {
				return errors.New("--host is required")
			}
			r, err := nodes.Load()
			if err != nil {
				return err
			}
			n := nodes.Node{Name: args[0], Host: host, SSH: sshFlag}
			r.Add(n)
			if err := r.Save(); err != nil {
				return err
			}
			fmt.Printf("Added %s -> %s\n", args[0], host)
			if t, err := sshTarget(n); err == nil {
				fmt.Printf("  ssh %s\n", t)
			}
			return nil
		},
	}
	c.Flags().StringVar(&host, "host", "", "base URL (scheme://host:port)")
	c.Flags().StringVar(&sshFlag, "ssh", "", "SSH target for orchestration (user@host)")
	return c
}

func newNodeListCmd() *cobra.Command {
	return &cobra.Command{Use: "ls", Short: "List nodes", RunE: func(*cobra.Command, []string) error {
		r, err := nodes.Load()
		if err != nil {
			return err
		}
		list := r.List()
		if len(list) == 0 {
			fmt.Println("No nodes.")
			return nil
		}
		printNodes(os.Stdout, list)
		return nil
	}}
}

func newNodeRemoveCmd() *cobra.Command {
	return &cobra.Command{Use: "rm <name>", Args: cobra.ExactArgs(1), Short: "Remove a node", RunE: func(_ *cobra.Command, args []string) error {
		r, err := nodes.Load()
		if err != nil {
			return err
		}
		r.Remove(args[0])
		return r.Save()
	}}
}

// printNodes lists each node with where it serves and how it is reached. The
// SSH column is what orchestration actually dials, including the target
// derived from --host when no --ssh was given, so a wrong target shows up
// here rather than as a failed launch.
func printNodes(w io.Writer, list []nodes.Node) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHOST\tSSH")
	for _, n := range list {
		target, err := sshTarget(n)
		switch {
		case err != nil:
			target = "-"
		case n.SSH == "":
			target += " (from --host)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", n.Name, n.Host, target)
	}
	_ = tw.Flush()
}
