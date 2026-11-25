package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"tiup-backup/internal/config"
	"tiup-backup/internal/cron"
	"tiup-backup/internal/remote"
)

func newJobCmd() *cobra.Command {
	var (
		schedule string
		disable  bool
	)
	c := &cobra.Command{
		Use:   "job [-s cron]",
		Short: "Show or set the scheduled `backup sync` (crontab)",
		Long: `Without flags, show the scheduled sync. With -s, install or replace it in the
current user's crontab. With --disable, remove it.

Cron cannot answer password prompts, so every backup node must accept key
authentication: either passwordless SSH you set up yourself (ssh-agent or a
default key in ~/.ssh) or an explicit "tiup backup add ... -i KEY". "job -s"
checks this by connecting to each node. Each node's host key must also be
trusted already (run "tiup backup sync" once by hand first).`,
		Example: `  tiup backup job
  tiup backup job -s "0 2 * * *"
  tiup backup job --disable`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := cron.Require(); err != nil {
				return err
			}
			if disable && schedule != "" {
				return errors.New("use either -s or --disable, not both")
			}
			home, err := config.Home()
			if err != nil {
				return err
			}
			lines, err := cron.ReadCrontab()
			if err != nil {
				return err
			}

			if disable {
				if _, ok := cron.Find(lines); !ok {
					fmt.Println("no scheduled sync to remove")
					return nil
				}
				if err := cron.WriteCrontab(cron.Replace(lines, "")); err != nil {
					return err
				}
				fmt.Println("scheduled sync removed")
				return nil
			}

			if schedule == "" {
				return showJob(home, lines)
			}
			return setJob(home, lines, schedule)
		},
	}
	c.Flags().StringVarP(&schedule, "schedule", "s", "", `cron expression, e.g. "0 2 * * *" or "@daily"`)
	c.Flags().BoolVar(&disable, "disable", false, "remove the scheduled sync")
	return c
}

func setJob(home string, lines []string, schedule string) error {
	expr, err := cron.Normalize(schedule)
	if err != nil {
		return err
	}
	nodes, err := config.LoadNodes(home)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return errors.New("no backup nodes yet; add some with `tiup backup add -N <ip,...> -i <key>` first")
	}

	// Cron cannot answer prompts: prove each node accepts key auth right now.
	var failed []string
	for _, n := range nodes {
		sshc, sc, err := remote.Connect(n, &remote.Credentials{}, config.KnownHostsFile(home), false, true)
		switch {
		case err == nil:
			sc.Close()
			sshc.Close()
			fmt.Printf("  %-18s key auth ok\n", n.Host)
		case remote.IsUnknownHost(err):
			fmt.Fprintf(os.Stderr, "  %-18s host key not trusted yet; run `tiup backup sync` once by hand, "+
				"otherwise the scheduled run will fail\n", n.Host)
		default:
			failed = append(failed, fmt.Sprintf("%s: %v", n.Host, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("scheduled sync needs unattended (key) access, but:\n  %s", strings.Join(failed, "\n  "))
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	line, err := cron.BuildLine(expr, home, exe, config.LogFile(home))
	if err != nil {
		return err
	}
	if err := cron.WriteCrontab(cron.Replace(lines, line)); err != nil {
		return err
	}
	fmt.Printf("scheduled sync set: %s\n", expr)
	fmt.Printf("log: %s\n", config.LogFile(home))
	return nil
}

func showJob(home string, lines []string) error {
	line, ok := cron.Find(lines)
	if !ok {
		fmt.Println("no scheduled sync; set one with `tiup backup job -s \"0 2 * * *\"`")
		return nil
	}
	logPath := config.LogFile(home)
	fmt.Printf("schedule: %s\n", cron.ParseSchedule(line))
	fmt.Printf("entry:    %s\n", line)
	fmt.Printf("log:      %s\n", logPath)
	if tail := cron.Tail(logPath, 3); len(tail) > 0 {
		fmt.Println("last log lines:")
		for _, l := range tail {
			fmt.Println("  " + l)
		}
	} else {
		fmt.Println("last log lines: (not run yet)")
	}
	return nil
}
