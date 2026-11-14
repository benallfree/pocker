// Package upgrade adds the `upgrade` command: it replaces the running executable with the latest PocketOne release
// and keeps the old one beside it as `<name>-<version>` (e.g. `pocketone-0.0.3`), so a project can keep running the
// engine it was built on while the CLI moves forward. Old copies are never deleted.
//
//	upgrade.MustRegister(app, app.RootCmd, upgrade.Config{})
package upgrade

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/archive"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// HTTPClient is the subset of *http.Client the plugin uses (tests swap it).
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config configures the plugin. Zero values pick PocketOne's own releases.
type Config struct {
	// Owner and Repo of the GitHub repository with the releases (default pockethost/pocketone).
	Owner, Repo string
	// ArchivePrefix is the release archive's name prefix, "<prefix>_<version>_<os>_<arch>.zip" (default "pocketone").
	ArchivePrefix string
	// ArchiveExecutable is the executable's name inside the archive (default "pocketone"; ".exe" is also tried).
	ArchiveExecutable string
	// BaseURL of the GitHub API (default https://api.github.com).
	BaseURL string
	// HTTPClient defaults to http.DefaultClient.
	HTTPClient HTTPClient
	// Context for fetching and downloading (default context.Background()).
	Context context.Context
}

func (c *Config) defaults() {
	if c.Owner == "" {
		c.Owner = "pockethost"
	}
	if c.Repo == "" {
		c.Repo = "pocketone"
	}
	if c.ArchivePrefix == "" {
		c.ArchivePrefix = "pocketone"
	}
	if c.ArchiveExecutable == "" {
		c.ArchiveExecutable = "pocketone"
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.github.com"
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.HTTPClient == nil {
		c.HTTPClient = http.DefaultClient
	}
	if c.Context == nil {
		c.Context = context.Background()
	}
}

// MustRegister is [Register] that panics on error.
func MustRegister(app core.App, rootCmd *cobra.Command, cfg Config) {
	if err := Register(app, rootCmd, cfg); err != nil {
		panic(err)
	}
}

// Register adds the `upgrade` command to rootCmd.
func Register(app core.App, rootCmd *cobra.Command, cfg Config) error {
	cfg.defaults()
	p := &plugin{app: app, cfg: cfg, current: rootCmd.Version, executable: os.Executable}
	rootCmd.AddCommand(p.command())
	return nil
}

type plugin struct {
	app        core.App
	cfg        Config
	current    string
	executable func() (string, error)
	goos       string // tests; default runtime.GOOS
	goarch     string
}

func (p *plugin) command() *cobra.Command {
	var backup bool
	cmd := &cobra.Command{
		Use:          "upgrade",
		Short:        "Upgrades this executable to the latest release, keeping the old one as <name>-<version>",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return p.upgrade(backup)
		},
	}
	cmd.Flags().BoolVar(&backup, "backup", true, "Back up pb_data after upgrading, if this folder has one")
	return cmd
}

func (p *plugin) upgrade(backup bool) error {
	exe, err := p.executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if brew := homebrewFormula(exe); brew != "" {
		color.Yellow("This executable was installed with Homebrew. Upgrade it with:")
		fmt.Printf("\n  brew upgrade %s\n\n", brew)
		color.HiBlack("Homebrew keeps the previous version until `brew cleanup`.")
		return nil
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return errors.New("this looks like a Docker container: upgrade the image instead")
	}

	color.Yellow("Fetching release information...")
	latest, err := p.cfg.FetchRelease(p.cfg.Context, "")
	if err != nil {
		return err
	}
	current := strings.TrimPrefix(p.current, "v")
	if isRelease(current) && compareVersions(current, latest.Version()) <= 0 {
		color.Green("You already have the latest version %s.", current)
		return nil
	}

	tmp, err := os.MkdirTemp(filepath.Dir(exe), ".upgrade-")
	if err != nil {
		return fmt.Errorf("can't write next to the executable (%s): %w", filepath.Dir(exe), err)
	}
	defer os.RemoveAll(tmp)

	goos, goarch := p.goos, p.goarch
	if goos == "" {
		goos, goarch = runtime.GOOS, runtime.GOARCH
	}
	color.Yellow("Downloading %s...", latest.Tag)
	zip, err := p.cfg.DownloadArchive(p.cfg.Context, latest, goos, goarch, tmp)
	if err != nil {
		return err
	}
	extracted := filepath.Join(tmp, "extracted")
	if err := archive.Extract(zip, extracted); err != nil {
		return err
	}
	newExe := filepath.Join(extracted, p.cfg.ArchiveExecutable)
	if _, err := os.Stat(newExe); err != nil {
		newExe += ".exe"
		if _, err2 := os.Stat(newExe); err2 != nil {
			return fmt.Errorf("the archive has no %s executable", p.cfg.ArchiveExecutable)
		}
	}

	kept := KeptName(exe, current)
	if err := os.Rename(exe, kept); err != nil {
		return fmt.Errorf("keeping the current executable as %s: %w", kept, err)
	}
	if err := os.Rename(newExe, exe); err != nil {
		if revertErr := os.Rename(kept, exe); revertErr != nil {
			return fmt.Errorf("replacing the executable: %w (and restoring it failed: %v; it is at %s)", err, revertErr, kept)
		}
		return fmt.Errorf("replacing the executable: %w", err)
	}

	if backup {
		if _, err := os.Stat(p.app.DataDir()); err == nil {
			color.Yellow("Backing up pb_data...")
			if err := p.app.CreateBackup(p.cfg.Context, fmt.Sprintf("@upgrade_%s.zip", latest.Tag)); err != nil {
				return fmt.Errorf("upgraded, but the pb_data backup failed: %w", err)
			}
		}
	}

	color.HiBlack("---")
	color.Green("Upgraded to %s.", latest.Version())
	fmt.Printf("The previous version is kept as %s\n", kept)
	if latest.Body != "" {
		fmt.Println()
		color.Cyan("Changes in %s:", latest.Tag)
		color.Cyan(strings.TrimSpace(latest.Body))
	}
	return nil
}

var releaseRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func isRelease(v string) bool { return releaseRe.MatchString(v) }

// KeptName is where upgrade keeps the executable at exe when it is version v: "<dir>/<name>-<v>[.exe]". A dev build
// (no release version) is kept as "<name>.old", which the next dev upgrade replaces.
func KeptName(exe, v string) string {
	dir, base := filepath.Split(exe)
	ext := ""
	if strings.EqualFold(filepath.Ext(base), ".exe") {
		ext = filepath.Ext(base)
		base = strings.TrimSuffix(base, ext)
	}
	v = strings.TrimPrefix(v, "v")
	if !isRelease(v) {
		return filepath.Join(dir, base+".old"+ext)
	}
	return filepath.Join(dir, base+"-"+v+ext)
}

// homebrewFormula returns the formula name when exe (symlinks resolved) is inside a Homebrew Cellar, else "".
func homebrewFormula(exe string) string {
	parts := strings.Split(filepath.ToSlash(exe), "/")
	for i, part := range parts {
		if part == "Cellar" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
