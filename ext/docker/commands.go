package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// RunSeed prints and seeds what src lists (`docker seed`), for a host's operator before switching it to docker.
func RunSeed(ctx context.Context, w io.Writer, src VersionsSource, b *Binaries) error {
	t0 := time.Now()
	listed, added, err := Seed(ctx, src, b)
	report(w, listed, added, b)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "seeded %d new of %d listed in %s\n", len(added), len(listed), time.Since(t0).Round(time.Millisecond))
	return nil
}

// RunUpdate finds the latest patch of each tracked minor, records and seeds them (`docker update`).
func RunUpdate(ctx context.Context, w io.Writer, d *Discovery, b *Binaries, tracked []string) error {
	t0 := time.Now()
	fmt.Fprintf(w, "checking %s for the latest patch of each minor\n", strings.Join(tracked, ", "))
	latest, added, err := Update(ctx, d, b, tracked)
	report(w, latest, added, b)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "tracking %d releases, %d new downloads, in %s\n", len(latest), len(added), time.Since(t0).Round(time.Millisecond))
	return nil
}

func report(w io.Writer, rs []Release, added []string, b *Binaries) {
	isNew := map[string]bool{}
	for _, k := range added {
		isNew[k] = true
	}
	for _, r := range rs {
		state := "missing"
		switch {
		case isNew[r.Key()]:
			state = "downloaded"
		case b.Has(r):
			state = "cached"
		}
		fmt.Fprintf(w, "  %-12s %s\n", r.Key(), state)
	}
}

// NewCommand returns `docker` with `seed` and `update` for a binary built on cobra (the stock pocketone binary). section
// returns the loaded `docker` section and the path resolver of the config file.
func NewCommand(section func() (SectionConfig, func(string) string)) *cobra.Command {
	cmd := &cobra.Command{Use: "docker", Short: "Release binaries for the docker runtime provider (docker.versions in pocketbase.yaml)"}
	binaries := func() (SectionConfig, *Binaries, error) {
		sc, resolve := section()
		if resolve == nil {
			resolve = func(s string) string { return s }
		}
		if sc.Binaries == "" {
			return sc, nil, fmt.Errorf("docker.binaries is not set")
		}
		return sc, &Binaries{Root: resolve(sc.Binaries)}, nil
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "seed",
		Short: "Downloads the releases docker.versions lists that the cache doesn't have yet",
		RunE: func(c *cobra.Command, _ []string) error {
			sc, b, err := binaries()
			if err != nil {
				return err
			}
			return RunSeed(c.Context(), c.OutOrStdout(), YAMLVersions{Tracked: sc.Versions, Root: b.Root}, b)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "update",
		Short: "Finds the latest patch of each docker.versions minor, records and downloads it",
		RunE: func(c *cobra.Command, _ []string) error {
			sc, b, err := binaries()
			if err != nil {
				return err
			}
			return RunUpdate(c.Context(), c.OutOrStdout(), DefaultDiscovery(), b, sc.Versions)
		},
	})
	return cmd
}
