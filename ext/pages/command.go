package pages

import (
	"bufio"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// The starter kits ship inside the binary (SPEC 11). all: keeps _private folders and dotfiles.
//
//go:embed all:starters
var startersFS embed.FS

// DefaultStarter is the kit `pages create` uses without --starter.
const DefaultStarter = "minimal"

// Starter is one embedded kit.
type Starter struct {
	Name  string // folder name, e.g. "daisyui-docs"
	Title string // README's first heading, e.g. "DaisyUI docs starter"
}

// Starters lists the embedded kits, sorted by name.
func Starters() []Starter {
	entries, _ := startersFS.ReadDir("starters")
	var out []Starter
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, Starter{Name: e.Name(), Title: readmeTitle(e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func readmeTitle(kit string) string {
	f, err := startersFS.Open(path.Join("starters", kit, "README.md"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return ""
}

// Create copies the starter kit into dir, which must not exist yet (SPEC 11).
func Create(dir, kit string) error {
	if kit == "" {
		kit = DefaultStarter
	}
	src := path.Join("starters", kit)
	if st, err := fs.Stat(startersFS, src); err != nil || !st.IsDir() {
		var names []string
		for _, s := range Starters() {
			names = append(names, s.Name)
		}
		return fmt.Errorf("no starter kit %q (kits: %s)", kit, strings.Join(names, ", "))
	}
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("%s already exists; pick a new folder name", dir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(startersFS, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, src), "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := startersFS.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// RegisterCommand adds `pages create` and `pages starters` to a PocketBase root command:
//
//	app := pocketbase.New()
//	pages.RegisterCommand(app.RootCmd)
func RegisterCommand(rootCmd *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "pages",
		Short: "PocketPages: create an app from a starter kit",
	}

	var kit string
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a folder with a starter kit (pb_pages and anything it needs)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			dir := args[0]
			if err := Create(dir, kit); err != nil {
				return err
			}
			if kit == "" {
				kit = DefaultStarter
			}
			w := c.OutOrStdout()
			fmt.Fprintf(w, "Created %s from the %s starter.\n\n", dir, kit)
			// --dir: PocketBase keeps pb_data next to its executable by default; pb_pages and pb_migrations are found
			// next to pb_data
			fmt.Fprintf(w, "  cd %s\n  %s serve --dir ./pb_data\n\n", dir, filepath.Base(os.Args[0]))
			fmt.Fprintf(w, "Then open http://127.0.0.1:8090. Edit files in %s and reload.\n",
				filepath.Join(dir, "pb_pages"))
			return nil
		},
	}
	create.Flags().StringVar(&kit, "starter", "", "starter kit (default "+DefaultStarter+"; list them with: pages starters)")
	create.SilenceUsage = true // a refusal prints the reason, not the help

	starters := &cobra.Command{
		Use:   "starters",
		Short: "List the starter kits",
		Args:  cobra.NoArgs,
		Run: func(c *cobra.Command, _ []string) {
			w := c.OutOrStdout()
			for _, s := range Starters() {
				def := ""
				if s.Name == DefaultStarter {
					def = " (default)"
				}
				fmt.Fprintf(w, "  %-14s %s%s\n", s.Name, s.Title, def)
			}
		},
	}

	cmd.AddCommand(create, starters)
	rootCmd.AddCommand(cmd)
}
