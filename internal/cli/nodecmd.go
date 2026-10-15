package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/nodes"
)

func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "node", Short: "Manage known nodes", GroupID: groupMachine}
	cmd.AddCommand(newNodeAddCmd(), newNodeListCmd(), newNodeRemoveCmd())
	return cmd
}

func newNodeAddCmd() *cobra.Command {
	var host, sshTarget string
	c := &cobra.Command{Use: "add <name>", Args: cobra.ExactArgs(1), Short: "Add a node",
		RunE: func(_ *cobra.Command, args []string) error {
			if host == "" {
				return errors.New("--host is required")
			}
			r, err := nodes.Load()
			if err != nil {
				return err
			}
			r.Add(nodes.Node{Name: args[0], Host: host, SSH: sshTarget})
			if err := r.Save(); err != nil {
				return err
			}
			fmt.Printf("Added %s -> %s\n", args[0], host)
			return nil
		},
	}
	c.Flags().StringVar(&host, "host", "", "base URL (scheme://host:port)")
	c.Flags().StringVar(&sshTarget, "ssh", "", "SSH target for orchestration (user@host)")
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
		for _, n := range list {
			fmt.Printf("%s	%s\n", n.Name, n.Host)
		}
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
