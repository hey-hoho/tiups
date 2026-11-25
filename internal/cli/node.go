package cli

import (
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/spf13/cobra"

	"tiup-backup/internal/config"
)

func newAddCmd() *cobra.Command {
	var (
		nodes     []string
		port      int
		userName  string
		identity  string
		remoteDir string
	)
	c := &cobra.Command{
		Use:   "add -N host1,host2",
		Short: "Add (or update) backup nodes",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if len(nodes) == 0 {
				return fmt.Errorf("-N is required")
			}
			home, err := config.Home()
			if err != nil {
				return err
			}
			cur, err := config.LoadNodes(home)
			if err != nil {
				return err
			}
			for _, h := range nodes {
				h = strings.TrimSpace(h)
				if h == "" {
					continue
				}
				n := config.Node{Host: h, Port: port, User: userName, IdentityFile: identity, RemoteDir: remoteDir}
				replaced := false
				for i := range cur {
					if cur[i].Host == h {
						cur[i], replaced = n, true
					}
				}
				if !replaced {
					cur = append(cur, n)
				}
				verb := "added"
				if replaced {
					verb = "updated"
				}
				fmt.Printf("%s %s@%s:%d\n", verb, n.User, n.Host, n.Port)
			}
			return config.SaveNodes(home, cur)
		},
	}
	def := "root"
	if u, err := user.Current(); err == nil {
		def = u.Username
	}
	c.Flags().StringSliceVarP(&nodes, "nodes", "N", nil, "backup node IPs/hostnames, comma separated")
	c.Flags().IntVarP(&port, "port", "P", 22, "SSH port")
	c.Flags().StringVarP(&userName, "user", "u", def, "SSH user")
	c.Flags().StringVarP(&identity, "identity-file", "i", "", "SSH private key (default: ssh-agent / ~/.ssh/id_*, then password)")
	c.Flags().StringVar(&remoteDir, "remote-dir", "", "destination directory on the node (default ~/.tiup)")
	return c
}

func newRemoveCmd() *cobra.Command {
	var nodes []string
	c := &cobra.Command{
		Use:   "remove -N host1,host2",
		Short: "Remove backup nodes",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if len(nodes) == 0 {
				return fmt.Errorf("-N is required")
			}
			home, err := config.Home()
			if err != nil {
				return err
			}
			cur, err := config.LoadNodes(home)
			if err != nil {
				return err
			}
			drop := map[string]bool{}
			for _, h := range nodes {
				drop[strings.TrimSpace(h)] = true
			}
			var left []config.Node
			for _, n := range cur {
				if drop[n.Host] {
					fmt.Println("removed", n.Host)
					delete(drop, n.Host)
					continue
				}
				left = append(left, n)
			}
			for h := range drop {
				fmt.Fprintf(os.Stderr, "warning: %s is not a backup node\n", h)
			}
			return config.SaveNodes(home, left)
		},
	}
	c.Flags().StringSliceVarP(&nodes, "nodes", "N", nil, "backup nodes to remove, comma separated")
	return c
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List backup nodes",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			home, err := config.Home()
			if err != nil {
				return err
			}
			cur, err := config.LoadNodes(home)
			if err != nil {
				return err
			}
			if len(cur) == 0 {
				fmt.Println("no backup nodes; use `tiup backup add -N <ip,...>`")
				return nil
			}
			fmt.Printf("%-20s %-6s %-12s %s\n", "HOST", "PORT", "USER", "AUTH")
			for _, n := range cur {
				auth := "default (agent/key/password)"
				if n.IdentityFile != "" {
					auth = "key:" + n.IdentityFile
				}
				fmt.Printf("%-20s %-6d %-12s %s\n", n.Host, n.Port, n.User, auth)
			}
			return nil
		},
	}
}
