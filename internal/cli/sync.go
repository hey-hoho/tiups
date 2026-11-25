package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"tiup-backup/internal/config"
	"tiup-backup/internal/lock"
	"tiup-backup/internal/remote"
	"tiup-backup/internal/syncer"
)

func newSyncCmd() *cobra.Command {
	var (
		only       []string
		del        bool
		dryRun     bool
		assumeYes  bool
		noPath     bool
		withMirror bool
	)
	c := &cobra.Command{
		Use:   "sync",
		Short: "Copy the whole local ~/.tiup to all backup nodes",
		Long: `Copy the whole local ~/.tiup (only this tool's per-machine state is skipped:
known_hosts, sync.log, sync.lock) to every backup node.

If tiup was set up from an offline package ("tiup mirror set <dir>"), tiup.toml
names that local directory, which is not inside ~/.tiup. Add --with-mirror to
copy it to the same path on each node as well.`,
		RunE: func(*cobra.Command, []string) error {
			home, err := config.Home()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(config.MetaDir(home), 0o700); err != nil {
				return err
			}
			unlock, err := lock.Acquire(config.LockFile(home))
			if err != nil {
				return err
			}
			defer unlock()

			targets, err := selectNodes(home, only)
			if err != nil {
				return err
			}

			patterns := syncer.DefaultExcludes
			entries, err := syncer.Scan(home, patterns)
			if err != nil {
				return fmt.Errorf("scan %s: %w", home, err)
			}
			suffix := ""
			if dryRun {
				suffix = " [dry-run]"
			}
			fmt.Printf("%s local %s: %d entries, %d node(s)%s\n", time.Now().Format(time.RFC3339), home, len(entries), len(targets), suffix)

			// An offline mirror lives outside ~/.tiup; --with-mirror copies it to the same path.
			mirrorDir := syncer.LocalMirror(home)
			var mirrorEntries []syncer.Entry
			if withMirror {
				if mirrorDir == "" {
					fmt.Println("--with-mirror: tiup.toml has no local mirror directory outside " + home + "; nothing extra to copy")
				} else {
					if mirrorEntries, err = syncer.Scan(mirrorDir, nil); err != nil {
						return fmt.Errorf("scan mirror %s: %w", mirrorDir, err)
					}
					fmt.Printf("local mirror %s: %d entries\n", mirrorDir, len(mirrorEntries))
				}
			}

			// Connect serially (password prompts), transfer in parallel.
			type result struct {
				home    syncer.Stats
				mirror  *syncer.Stats
				warning string
			}
			type session struct {
				node config.Node
				run  func() (result, error)
				done func()
			}
			creds := &remote.Credentials{}
			var sessions []session
			var failures []string
			for _, n := range targets {
				sshc, sc, err := remote.Connect(n, creds, config.KnownHostsFile(home), assumeYes, false)
				if err != nil {
					failures = append(failures, fmt.Sprintf("%s: %v", n.Host, err))
					continue
				}
				n := n
				sessions = append(sessions, session{
					node: n,
					done: func() { sc.Close(); sshc.Close() },
					run: func() (result, error) {
						var r result
						var err error
						r.home, err = syncer.SyncNode(sc, entries, syncer.Options{
							LocalRoot: home, RemoteDir: n.RemoteDir,
							Patterns: patterns, Delete: del, DryRun: dryRun,
							SetPath: !noPath, SSHClient: sshc,
						}, os.Stdout)
						if err != nil {
							return r, err
						}
						switch {
						case withMirror && mirrorDir != "":
							ms, err := syncer.SyncNode(sc, mirrorEntries, syncer.Options{
								LocalRoot: mirrorDir, RemoteDir: mirrorDir,
								Delete: del, DryRun: dryRun,
							}, os.Stdout)
							if err != nil {
								return r, fmt.Errorf("mirror %s: %w", mirrorDir, err)
							}
							r.mirror = &ms
						case !withMirror:
							r.warning = syncer.MirrorWarning(sc, mirrorDir)
						}
						return r, nil
					},
				})
			}

			var wg sync.WaitGroup
			var mu sync.Mutex
			for _, s := range sessions {
				wg.Add(1)
				go func(s session) {
					defer wg.Done()
					defer s.done()
					r, err := s.run()
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						failures = append(failures, fmt.Sprintf("%s: %v", s.node.Host, err))
						return
					}
					st := r.home
					fmt.Printf("%-18s uploaded=%d (%.1f MiB) unchanged=%d deleted=%d failed=%d\n",
						s.node.Host, st.Uploaded, float64(st.Bytes)/1048576, st.Skipped, st.Deleted, st.Failed)
					if st.Failed > 0 {
						failures = append(failures, fmt.Sprintf("%s: %d file(s) failed", s.node.Host, st.Failed))
					}
					if m := r.mirror; m != nil {
						fmt.Printf("%-18s mirror %s: uploaded=%d (%.1f MiB) unchanged=%d deleted=%d failed=%d\n",
							"", mirrorDir, m.Uploaded, float64(m.Bytes)/1048576, m.Skipped, m.Deleted, m.Failed)
						if m.Failed > 0 {
							failures = append(failures, fmt.Sprintf("%s: %d mirror file(s) failed", s.node.Host, m.Failed))
						}
					}
					if st.Note != "" {
						fmt.Printf("%-18s PATH: %s\n", "", st.Note)
					}
					if r.warning != "" {
						fmt.Printf("%-18s WARNING: %s\n", "", r.warning)
					}
				}(s)
			}
			wg.Wait()

			if len(failures) > 0 {
				return fmt.Errorf("sync incomplete:\n  %s", strings.Join(failures, "\n  "))
			}
			return nil
		},
	}
	c.Flags().StringSliceVarP(&only, "nodes", "N", nil, "only sync these backup nodes (default: all)")
	c.Flags().BoolVar(&withMirror, "with-mirror", false, "also copy the local offline mirror directory named in tiup.toml to the same path on the node")
	c.Flags().BoolVar(&del, "delete", false, "delete files on the node that no longer exist locally")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change")
	c.Flags().BoolVar(&assumeYes, "yes", false, "trust unknown SSH host keys without asking")
	c.Flags().BoolVar(&noPath, "no-path", false, "do not add the node's ~/.tiup/bin to PATH in its shell profile")
	return c
}

// selectNodes returns the configured nodes, narrowed to only (when given).
func selectNodes(home string, only []string) ([]config.Node, error) {
	all, err := config.LoadNodes(home)
	if err != nil {
		return nil, err
	}
	targets := all
	if len(only) > 0 {
		want := map[string]bool{}
		for _, h := range only {
			want[strings.TrimSpace(h)] = true
		}
		targets = nil
		for _, n := range all {
			if want[n.Host] {
				targets = append(targets, n)
				delete(want, n.Host)
			}
		}
		for h := range want {
			return nil, fmt.Errorf("%s is not a backup node (see `tiup backup list`)", h)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no backup nodes configured")
	}
	return targets, nil
}
