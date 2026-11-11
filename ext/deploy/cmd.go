package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pockethost/pocketbase/ext/deploy/internal/glob"
	"github.com/pockethost/pocketbase/ext/remotes"
	"github.com/spf13/cobra"
)

// Commands returns the client commands: deploy, dev, pull, logs.
func Commands() []*cobra.Command {
	return []*cobra.Command{deployCommand(), devCommand(), pullCommand(), logsCommand()}
}

type setFlags struct {
	include       []string
	exclude       []string
	noNodeModules bool
	nodeModules   bool
	verbose       bool
}

func (f *setFlags) bind(c *cobra.Command) {
	c.Flags().StringSliceVarP(&f.include, "include", "i", nil, "add include patterns (default set: "+strings.Join(DefaultIncludes, ",")+")")
	c.Flags().StringSliceVarP(&f.exclude, "exclude", "e", nil, "exclude patterns (pb_data is always excluded)")
	c.Flags().BoolVar(&f.nodeModules, "node-modules", false, "ship the production node_modules tree (default with a package.json)")
	c.Flags().BoolVar(&f.noNodeModules, "no-node-modules", false, "don't ship node_modules; leave the remote's alone")
	c.Flags().BoolVarP(&f.verbose, "verbose", "v", false, "list every file")
}

func (f *setFlags) collect(dir string) CollectOptions {
	o := CollectOptions{Dir: dir, Exclude: f.exclude}
	if len(f.include) > 0 {
		o.Include = append(append([]string{}, DefaultIncludes...), f.include...)
	}
	switch {
	case f.noNodeModules:
		v := false
		o.NodeModules = &v
	case f.nodeModules:
		v := true
		o.NodeModules = &v
	}
	return o
}

func projectRoot() string {
	d, _ := os.Getwd()
	return remotes.FindProjectRoot(d)
}

func open(ctx context.Context, remote string) (*Client, error) {
	rc, err := remotes.Open(projectRoot(), remote)
	if err != nil {
		return nil, err
	}
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := NewClient(wctx, rc)
	if err != nil {
		return nil, err
	}
	// fail fast on a bad login before collecting files
	if _, err := c.DeployWhoami(wctx); err != nil {
		return nil, err
	}
	return c, nil
}

func printer(w io.Writer) func(string) { return func(s string) { fmt.Fprintln(w, s) } }

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func deployCommand() *cobra.Command {
	var (
		f       setFlags
		commit  string
		message string
		out     string
		dryRun  bool
	)
	c := &cobra.Command{
		Use:   "deploy [remote]",
		Short: "Deploys pb_hooks, pb_migrations, pb_public and packages to a remote",
		Long: "Deploys the project's code set (pb_* folders except pb_data, package.json, bun.lock(b), patches/, and the " +
			"production node_modules tree) to a remote. Only changed files are uploaded; the remote swaps them in and " +
			"restarts the app when hooks, migrations or packages changed.",
		Args: cobra.MaximumNArgs(1), SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			log := printer(cmd.OutOrStdout())
			opts := f.collect(projectRoot())
			if dryRun || out != "" {
				set, err := Collect(opts)
				if err != nil {
					return err
				}
				for _, l := range set.Describe() {
					log(l)
				}
				if err := set.CheckComplete(); err != nil {
					return err
				}
				if dryRun {
					var total int64
					for _, fl := range set.Files {
						if f.verbose || !strings.HasPrefix(fl.Path, "node_modules/") {
							log("   " + fl.Path)
						}
						total += fl.Size
					}
					log(fmt.Sprintf("Would deploy %d files (%s).", len(set.Files), humanBytes(total)))
					return nil
				}
				if err := set.WriteTo(out); err != nil {
					return err
				}
				log(fmt.Sprintf("Wrote %d files to %s.", len(set.Files), out))
				return nil
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			c, err := open(ctx, firstArg(args))
			if err != nil {
				return err
			}
			_, err = Deploy(ctx, c, DeployOptions{CollectOptions: opts, Commit: commit, Message: message, Verbose: f.verbose, Log: log})
			return err
		},
	}
	f.bind(c)
	c.Flags().StringVar(&commit, "commit", "", "commit SHA to record (default: $GITHUB_SHA or git HEAD)")
	c.Flags().StringVarP(&message, "message", "m", "", "note to record with this deploy")
	c.Flags().StringVar(&out, "out", "", "write the deploy set to a local folder instead of deploying")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "list what would be deployed, deploy nothing")
	return c
}

func devCommand() *cobra.Command {
	var (
		f      setFlags
		noLogs bool
	)
	c := &cobra.Command{
		Use:   "dev [remote]",
		Short: "Deploys now, then again on every change (and streams the remote's logs)",
		Args:  cobra.MaximumNArgs(1), SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			out := cmd.OutOrStdout()
			log := printer(out)
			c, err := open(ctx, firstArg(args))
			if err != nil {
				return err
			}
			opts := f.collect(projectRoot())
			if !noLogs {
				go followLogs(ctx, c, 0, out)
			}
			return Watch(ctx, opts, 500*time.Millisecond, log, func() {
				if _, err := Deploy(ctx, c, DeployOptions{CollectOptions: opts, Message: "dev", Verbose: f.verbose, Log: log}); err != nil && ctx.Err() == nil {
					log("Deploy failed: " + err.Error())
				}
			})
		},
	}
	f.bind(c)
	c.Flags().BoolVar(&noLogs, "no-logs", false, "don't stream the remote's logs")
	return c
}

// Watch calls deploy once, then again (debounced) whenever a file of the deploy set changes, until ctx ends.
func Watch(ctx context.Context, opts CollectOptions, debounce time.Duration, log func(string), deploy func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	include := opts.Include
	if len(include) == 0 {
		include = DefaultIncludes
	}
	root, _ := filepath.Abs(opts.Dir)
	watchable := func(rel string) bool {
		if rel == "." {
			return true
		}
		name := filepath.Base(rel)
		if name == "pb_data" || name == "node_modules" || strings.HasPrefix(name, ".git") {
			return false // a package install changes package.json or the lockfile too
		}
		return glob.AnyUnder(include, rel)
	}
	addTree := func(dir string) {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if !watchable(rel) {
				return filepath.SkipDir
			}
			_ = w.Add(p)
			return nil
		})
	}
	addTree(root)

	var (
		mu    sync.Mutex
		timer *time.Timer
		busy  sync.Mutex
	)
	trigger := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(debounce, func() {
			busy.Lock()
			defer busy.Unlock()
			deploy()
		})
	}
	busy.Lock()
	deploy()
	busy.Unlock()
	log("Watching for changes (Ctrl+C to stop)…")
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-w.Errors:
			if err != nil {
				log("watch: " + err.Error())
			}
		case ev, ok := <-w.Events:
			if !ok {
				return errors.New("watcher closed")
			}
			rel, _ := filepath.Rel(root, ev.Name)
			rel = filepath.ToSlash(rel)
			if ev.Op&fsnotify.Create != 0 {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
					addTree(ev.Name)
				}
			}
			if strings.HasPrefix(rel, "pb_data/") || strings.Contains("/"+rel, "/.git") {
				continue
			}
			trigger()
		}
	}
}

func pullCommand() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:          "pull [remote] [paths...]",
		Short:        "Downloads files from a remote into the project (default: pb_hooks, pb_migrations, pb_public, package.json)",
		Example:      "pull prod\npull prod pb_hooks pb_public/index.html",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			c, err := open(ctx, firstArg(args))
			if err != nil {
				return err
			}
			var paths []string
			if len(args) > 1 {
				paths = args[1:]
			}
			log := printer(cmd.OutOrStdout())
			n, err := Pull(ctx, c, PullOptions{Dir: projectRoot(), Paths: paths, DryRun: dryRun, Log: log})
			if err != nil {
				return err
			}
			if dryRun {
				log(fmt.Sprintf("Would pull %d file(s).", n))
			} else {
				log(fmt.Sprintf("Pulled %d file(s) from %s.", n, c.Remote.URL))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "list what would be pulled")
	return c
}

func logsCommand() *cobra.Command {
	var n int
	c := &cobra.Command{
		Use:   "logs [remote]",
		Short: "Streams a remote's app logs",
		Args:  cobra.MaximumNArgs(1), SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			c, err := open(ctx, firstArg(args))
			if err != nil {
				return err
			}
			followLogs(ctx, c, n, cmd.OutOrStdout())
			return nil
		},
	}
	c.Flags().IntVarP(&n, "lines", "n", 100, "replay this many earlier lines (0..1000)")
	return c
}

// followLogs streams logs and reconnects (without replay) when the stream drops, until ctx ends.
func followLogs(ctx context.Context, c *Client, n int, out io.Writer) {
	for ctx.Err() == nil {
		err := c.StreamLogs(ctx, n, func(e LogEntry) {
			prefix := ""
			if e.Stream == "stderr" {
				prefix = "ERR "
			}
			ts := e.Time
			if t, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
				ts = t.Local().Format("15:04:05")
			}
			fmt.Fprintf(out, "%s %s%s\n", ts, prefix, strings.TrimRight(e.Message, "\n"))
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			fmt.Fprintf(out, "log stream: %v (reconnecting)\n", err)
		}
		n = 0
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}
