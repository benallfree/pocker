package tokens

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

// NewCommand returns the `token` command (create, list, revoke) for headless setup. It works on the local data dir,
// like `superuser`.
func NewCommand(t *Tokens) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens (pbt_...)",
	}
	cmd.AddCommand(tokenCreateCommand(t), tokenListCommand(t), tokenRevokeCommand(t), tokenScopesCommand(t))
	return cmd
}

// ownerFlags picks a token owner: --superuser email, or --collection + --record (id or email).
type ownerFlags struct {
	superuser  string
	collection string
	record     string
}

func (o *ownerFlags) bind(c *cobra.Command) {
	c.Flags().StringVar(&o.superuser, "superuser", "", "owner: a superuser's email")
	c.Flags().StringVar(&o.collection, "collection", "", "owner: auth collection (with --record)")
	c.Flags().StringVar(&o.record, "record", "", "owner: record id or email in --collection")
}

func (o *ownerFlags) find(t *Tokens, required bool) (*core.Record, error) {
	switch {
	case o.superuser != "":
		return t.findOwner(core.CollectionNameSuperusers, o.superuser)
	case o.record != "":
		if o.collection == "" {
			return nil, errors.New("--record needs --collection")
		}
		return t.findOwner(o.collection, o.record)
	case required:
		return nil, errors.New("pick an owner: --superuser <email>, or --collection <name> --record <id|email>")
	}
	return nil, nil
}

func tokenCreateCommand(t *Tokens) *cobra.Command {
	var (
		owner  ownerFlags
		name   string
		scopes []string
		ttl    time.Duration
	)
	c := &cobra.Command{
		Use:          "create",
		Example:      "token create --superuser admin@example.com --name ci --scope deploy",
		Short:        "Creates an API token and prints its secret (shown once)",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			rec, err := owner.find(t, true)
			if err != nil {
				return fmt.Errorf("owner: %w", err)
			}
			opts := CreateOptions{Name: name, Scopes: scopes}
			if ttl > 0 {
				opts.Expires = t.cfg.Now().Add(ttl)
			}
			secret, _, err := t.Create(rec, opts)
			if err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), secret)
			return nil
		},
	}
	owner.bind(c)
	c.Flags().StringVar(&name, "name", "", "a name to recognize the token by")
	c.Flags().StringSliceVar(&scopes, "scope", nil, "limit the token to a scope (repeatable; none: the owner's full rights)")
	c.Flags().DurationVar(&ttl, "ttl", 0, "lifetime, e.g. 720h (default: never expires)")
	return c
}

func tokenListCommand(t *Tokens) *cobra.Command {
	var owner ownerFlags
	c := &cobra.Command{
		Use:          "list",
		Short:        "Lists API tokens (all, or one owner's)",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			rec, err := owner.find(t, false)
			if err != nil {
				return fmt.Errorf("owner: %w", err)
			}
			recs, err := t.List(rec)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tPREFIX\tNAME\tOWNER\tSCOPES\tEXPIRES\tLAST USED")
			for _, r := range recs {
				v := t.View(r)
				sc := strings.Join(v.Scopes, ",")
				if sc == "" {
					sc = "(all)"
				}
				fmt.Fprintf(w, "%s\t%s…\t%s\t%s/%s\t%s\t%s\t%s\n", v.Id, v.Prefix, v.Name, v.OwnerCollection,
					orDash(v.OwnerEmail, v.Owner), sc, orDash(v.Expires, "never"), orDash(v.LastUsed, "-"))
			}
			return w.Flush()
		},
	}
	owner.bind(c)
	return c
}

func tokenRevokeCommand(t *Tokens) *cobra.Command {
	var (
		owner ownerFlags
		all   bool
	)
	c := &cobra.Command{
		Use:          "revoke [id...]",
		Example:      "token revoke abc123\ntoken revoke --all --superuser admin@example.com",
		Short:        "Revokes API tokens by id, or all of one owner's (--all)",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if all {
				rec, err := owner.find(t, true)
				if err != nil {
					return fmt.Errorf("owner: %w", err)
				}
				n, err := t.RevokeAll(rec)
				if err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "Revoked %d token(s).\n", n)
				return nil
			}
			if len(args) == 0 {
				return errors.New("give token ids, or --all with an owner")
			}
			for _, id := range args {
				if err := t.Revoke(id); err != nil {
					return fmt.Errorf("%s: %w", id, err)
				}
			}
			fmt.Fprintf(c.OutOrStdout(), "Revoked %d token(s).\n", len(args))
			return nil
		},
	}
	owner.bind(c)
	c.Flags().BoolVar(&all, "all", false, "revoke every token of the owner")
	return c
}

func tokenScopesCommand(t *Tokens) *cobra.Command {
	return &cobra.Command{
		Use:          "scopes",
		Short:        "Lists the scopes plugins registered",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "SCOPE\tDESCRIPTION\tPATHS")
			for _, s := range t.Scopes() {
				fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, s.Description, strings.Join(s.Paths, " "))
			}
			return w.Flush()
		},
	}
}

func orDash(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
