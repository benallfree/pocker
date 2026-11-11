package oauthserver

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// NewCommand returns `oauth clients list|add|rm`.
func NewCommand(s *Server) *cobra.Command {
	cmd := &cobra.Command{Use: "oauth", Short: "Manage the OAuth server"}
	clients := &cobra.Command{Use: "clients", Short: "Manage registered OAuth clients"}

	list := &cobra.Command{
		Use: "list", Short: "Lists registered clients", SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			cl, err := s.ListClients()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "CLIENT ID\tNAME\tTYPE\tREDIRECT URIS")
			fmt.Fprintf(w, "%s\t%s\tbuilt-in\tany loopback, device codes\n", CLIClient.Id, CLIClient.Name)
			for _, x := range cl {
				kind := "confidential"
				if x.Public {
					kind = "public"
				}
				if x.Dynamic {
					kind += ", registered"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", x.Id, x.Name, kind, strings.Join(x.RedirectURIs, " "))
			}
			return w.Flush()
		},
	}

	var (
		name     string
		redirect []string
		public   bool
	)
	add := &cobra.Command{
		Use: "add", Short: "Registers a client (prints its id, and its secret once for a confidential client)", SilenceUsage: true,
		Example: "oauth clients add --name \"My app\" --redirect https://myapp.example.com/callback\n" +
			"oauth clients add --name \"Desktop\" --redirect http://127.0.0.1/callback --public",
		RunE: func(c *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			cl, secret, err := s.AddClient(name, redirect, public, false)
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "client_id: %s\n", cl.Id)
			if secret != "" {
				fmt.Fprintf(c.OutOrStdout(), "client_secret: %s\n(shown once)\n", secret)
			}
			return nil
		},
	}
	add.Flags().StringVar(&name, "name", "", "name shown on the consent page")
	add.Flags().StringSliceVar(&redirect, "redirect", nil, "allowed redirect URI (repeatable; loopback URIs match any port)")
	add.Flags().BoolVar(&public, "public", false, "public client (no secret; PKCE only), e.g. a desktop or mobile app")

	rm := &cobra.Command{
		Use: "rm <client_id>", Short: "Removes a client", Args: cobra.ExactArgs(1), SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error { return s.RemoveClient(args[0]) },
	}

	clients.AddCommand(list, add, rm)

	var force bool
	eject := &cobra.Command{
		Use:   "eject",
		Short: "Writes the built-in OAuth pages into pb_pages as PocketPages templates you can edit",
		Long: "Writes the sign-in, one-time code, consent, device code, done and error pages, and their shared " +
			"head/foot partials, to <pb_pages>/" + EjectSubdir + ". From then on the server renders those templates " +
			"(through ext/pages); delete them to go back to the built-in pages.",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			dir, ok := s.PagesDir()
			if !ok {
				return errNoPages
			}
			written, err := Eject(dir, force)
			if err != nil {
				return err
			}
			for _, p := range written {
				fmt.Fprintln(c.OutOrStdout(), "wrote "+p)
			}
			fmt.Fprintf(c.OutOrStdout(), "Ejected %d file(s). The OAuth pages now render from them.\n", len(written))
			return nil
		},
	}
	eject.Flags().BoolVar(&force, "force", false, "overwrite existing files")

	cmd.AddCommand(clients, eject)
	return cmd
}
