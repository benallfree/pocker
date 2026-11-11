package remotes

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// BrowserLogin, when set (by the OAuth client half), is tried first by `login` on remotes that offer OAuth. It
// returns (nil, nil) to fall back to the token/password prompt.
var BrowserLogin func(ctx context.Context, c *Client, w *Whoami, opts BrowserLoginOptions) (*LoginResult, error)

// BrowserLoginOptions are `login`'s flags for the OAuth flow.
type BrowserLoginOptions struct {
	Device    bool // device code instead of a browser redirect
	NoBrowser bool // print the URL, don't open a browser
	Out       io.Writer
}

// Commands returns the client commands: remote, login, logout, link.
func Commands() []*cobra.Command {
	return []*cobra.Command{remoteCommand(), loginCommand(), logoutCommand(), linkCommand()}
}

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

func remoteCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "remote", Short: "Manage the project's remotes (.pocketbase/remotes.json)"}

	var makeDefault bool
	add := &cobra.Command{
		Use: "add <name> <url>", Short: "Adds or replaces a remote", Args: cobra.ExactArgs(2), SilenceUsage: true,
		Example: "remote add prod https://myapp.example.com",
		RunE: func(c *cobra.Command, args []string) error {
			p, err := LoadProject(cwd())
			if err != nil {
				return err
			}
			r, err := p.Add(args[0], args[1])
			if err != nil {
				return err
			}
			if makeDefault {
				p.Default = r.Name
			}
			if err := p.Save(); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Added %s → %s. Next: pocketbase login %s\n", r.Name, r.URL, r.Name)
			return nil
		},
	}
	add.Flags().BoolVar(&makeDefault, "default", false, "make it the default remote")

	list := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lists remotes and whether you're logged in", SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			p, err := LoadProject(cwd())
			if err != nil {
				return err
			}
			creds, err := LoadCredentials()
			if err != nil {
				return err
			}
			if len(p.Remotes) == 0 {
				fmt.Fprintln(c.OutOrStdout(), "No remotes. Add one: pocketbase remote add prod https://myapp.example.com")
				return nil
			}
			w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "\tNAME\tURL\tLOGIN")
			for _, n := range p.Names() {
				r := p.Remotes[n]
				mark := ""
				if n == p.Default {
					mark = "*"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", mark, n, r.URL, describeLogin(n, creds.ByURL[r.URL]))
			}
			return w.Flush()
		},
	}

	rm := &cobra.Command{
		Use: "rm <name>", Aliases: []string{"remove"}, Short: "Removes a remote (its saved login stays; see logout)",
		Args: cobra.ExactArgs(1), SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			p, err := LoadProject(cwd())
			if err != nil {
				return err
			}
			if err := p.Remove(args[0]); err != nil {
				return err
			}
			return p.Save()
		},
	}

	def := &cobra.Command{
		Use: "default <name>", Short: "Sets the default remote", Args: cobra.ExactArgs(1), SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			p, err := LoadProject(cwd())
			if err != nil {
				return err
			}
			if _, ok := p.Remotes[args[0]]; !ok {
				return fmt.Errorf("no remote %q", args[0])
			}
			p.Default = args[0]
			return p.Save()
		},
	}

	whoami := &cobra.Command{
		Use: "whoami [remote]", Short: "Shows who you are on a remote and what it offers", Args: cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			cl, err := Open(cwd(), first(args))
			if err != nil && cl == nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), 20*time.Second)
			defer cancel()
			w, werr := cl.Whoami(ctx)
			if werr != nil {
				return werr
			}
			out := c.OutOrStdout()
			fmt.Fprintf(out, "%s (%s)\n", cl.Remote.Name, cl.Remote.URL)
			if w.Auth == nil {
				fmt.Fprintln(out, "  not logged in")
			} else {
				sc := "all"
				if len(w.Scopes) > 0 {
					sc = strings.Join(w.Scopes, ", ")
				}
				fmt.Fprintf(out, "  %s in %s, via %s (%s), scopes: %s\n", w.Auth.Email, w.Auth.Collection, w.Via, cl.TokenSource, sc)
			}
			for k, v := range w.Features {
				fmt.Fprintf(out, "  offers %s at %s\n", k, v)
			}
			return nil
		},
	}

	cmd.AddCommand(add, list, rm, def, whoami)
	return cmd
}

func describeLogin(name string, cr *Credential) string {
	if tok, src := EnvToken(name); tok != "" {
		return src
	}
	if cr == nil {
		return "-"
	}
	s := cr.Via
	if cr.Email != "" {
		s = cr.Email + " (" + cr.Via + ")"
	}
	if !cr.Expires.IsZero() {
		if time.Now().After(cr.Expires) {
			s += ", expired"
		} else {
			s += ", until " + cr.Expires.Format("2006-01-02")
		}
	}
	return s
}

func first(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

type loginFlags struct {
	token      string
	tokenStdin bool
	email      string
	collection string
	device     bool
	noBrowser  bool
	password   bool
}

func (f *loginFlags) bind(c *cobra.Command) {
	c.Flags().StringVar(&f.token, "token", "", "use this API token (pbt_..., a PocketHost token, or a JWT)")
	c.Flags().BoolVar(&f.tokenStdin, "token-stdin", false, "read the token from stdin")
	c.Flags().StringVar(&f.email, "email", "", "sign in with this email and a password")
	c.Flags().StringVar(&f.collection, "collection", "_superusers", "auth collection for email sign-in")
	c.Flags().BoolVar(&f.password, "password", false, "sign in with email + password instead of the browser")
	c.Flags().BoolVar(&f.device, "device", false, "sign in on another device with a code (SSH, headless)")
	c.Flags().BoolVar(&f.noBrowser, "no-browser", false, "print the sign-in URL instead of opening a browser")
}

func loginCommand() *cobra.Command {
	var f loginFlags
	c := &cobra.Command{
		Use:   "login [remote]",
		Short: "Signs in to a remote and saves the login",
		Long: "Signs in to a remote: in the browser when the remote offers OAuth (--device for a code on another device), " +
			"else with a pasted API token, else with email + password (mints an API token; the remote needs the tokens plugin).",
		Args: cobra.MaximumNArgs(1), SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			cl, err := Open(cwd(), first(args))
			if err != nil && cl == nil {
				return err
			}
			return runLogin(c, cl, f)
		},
	}
	f.bind(c)
	return c
}

func linkCommand() *cobra.Command {
	var (
		f    loginFlags
		name string
	)
	c := &cobra.Command{
		Use:     "link <url> [token]",
		Short:   "Adds a remote (default name \"default\") and signs in to it",
		Example: "link https://myapp.example.com\nlink https://myapp.pockethost.io ph_pat_...",
		Args:    cobra.RangeArgs(1, 2), SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			p, err := LoadProject(cwd())
			if err != nil {
				return err
			}
			r, err := p.Add(name, args[0])
			if err != nil {
				return err
			}
			if p.Default == "" || len(p.Remotes) == 1 {
				p.Default = r.Name
			}
			if err := p.Save(); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Linked %s → %s\n", r.Name, r.URL)
			if len(args) == 2 {
				f.token = args[1]
			}
			return runLogin(c, &Client{Remote: r, HTTP: NewHTTPClient()}, f)
		},
	}
	c.Flags().StringVar(&name, "name", DefaultName, "remote name")
	f.bind(c)
	return c
}

func runLogin(c *cobra.Command, cl *Client, f loginFlags) error {
	out := c.OutOrStdout()
	in := bufio.NewReader(c.InOrStdin())
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Minute)
	defer cancel()
	cl.Token, cl.TokenSource = "", ""

	var (
		res *LoginResult
		err error
	)
	switch {
	case f.tokenStdin:
		line, rerr := in.ReadString('\n')
		if rerr != nil && rerr != io.EOF {
			return rerr
		}
		res, err = LoginWithToken(ctx, cl, line)
	case f.token != "":
		res, err = LoginWithToken(ctx, cl, f.token)
	case f.email != "" || f.password:
		res, err = passwordLogin(ctx, cl, f, in, out)
	default:
		// OAuth first, when the remote offers it and this binary has the client half
		if BrowserLogin != nil {
			wctx, wcancel := context.WithTimeout(ctx, 20*time.Second)
			w, werr := cl.Whoami(wctx)
			wcancel()
			if werr != nil && !errors.Is(werr, ErrNoWhoami) {
				return werr
			}
			if w != nil && w.Features["oauth"] != "" {
				res, err = BrowserLogin(ctx, cl, w, BrowserLoginOptions{Device: f.device, NoBrowser: f.noBrowser, Out: out})
				if err != nil {
					return err
				}
			}
		}
		if res == nil {
			if !isTerminal(c.InOrStdin()) {
				return errors.New("no terminal to prompt in: pass --token, --token-stdin, or set POCKETBASE_TOKEN")
			}
			fmt.Fprintf(out, "Signing in to %s.\nPaste an API token, or enter a superuser email to sign in with a password: ", cl.Remote.URL)
			line, rerr := in.ReadString('\n')
			if rerr != nil && rerr != io.EOF {
				return rerr
			}
			line = strings.TrimSpace(line)
			if strings.Contains(line, "@") && !looksLikeToken(line) {
				f.email = line
				res, err = passwordLogin(ctx, cl, f, in, out)
			} else {
				res, err = LoginWithToken(ctx, cl, line)
			}
		}
	}
	if err != nil {
		return err
	}
	if err := SaveLogin(cl, res); err != nil {
		return err
	}
	who := res.Credential.Email
	if who == "" {
		who = "token"
	}
	fmt.Fprintf(out, "Logged in to %s as %s (%s).\n", cl.Remote.URL, who, res.Credential.Via)
	if res.Warning != "" {
		fmt.Fprintln(out, "Note: "+res.Warning)
	}
	return nil
}

func looksLikeToken(s string) bool {
	isJWT := strings.Count(s, ".") == 2 && !strings.Contains(s, "@")
	return strings.HasPrefix(s, "pbt_") || strings.HasPrefix(s, "ph_pat_") || isJWT
}

func passwordLogin(ctx context.Context, cl *Client, f loginFlags, in *bufio.Reader, out io.Writer) (*LoginResult, error) {
	email := f.email
	if email == "" {
		fmt.Fprint(out, "Email: ")
		line, err := in.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		email = strings.TrimSpace(line)
	}
	password := os.Getenv("POCKETBASE_PASSWORD")
	if password == "" {
		fmt.Fprint(out, "Password: ")
		p, err := readSecret(in)
		fmt.Fprintln(out)
		if err != nil {
			return nil, err
		}
		password = p
	}
	return LoginWithPassword(ctx, cl, PasswordLogin{
		Collection: f.collection, Email: email, Password: password,
		OTP: func(email string) (string, error) {
			fmt.Fprintf(out, "A one-time code was sent to %s. Code: ", email)
			line, err := in.ReadString('\n')
			if err != nil && err != io.EOF {
				return "", err
			}
			return strings.TrimSpace(line), nil
		},
	})
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func readSecret(in *bufio.Reader) (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) && in.Buffered() == 0 {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		return string(b), err
	}
	line, err := in.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use: "logout [remote]", Short: "Forgets the saved login (and revokes the API token login created)",
		Args: cobra.MaximumNArgs(1), SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			p, err := LoadProject(cwd())
			if err != nil {
				return err
			}
			r, err := p.Resolve(first(args))
			if err != nil {
				return err
			}
			creds, err := LoadCredentials()
			if err != nil {
				return err
			}
			cr := creds.ByURL[r.URL]
			if cr == nil {
				fmt.Fprintf(c.OutOrStdout(), "Not logged in to %s.\n", r.URL)
				return nil
			}
			if cr.Via == "oauth" {
				ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
				if err := revokeOAuth(ctx, &Client{Remote: r, HTTP: NewHTTPClient()}, cr); err != nil {
					fmt.Fprintf(c.ErrOrStderr(), "Couldn't revoke the login on %s (%v); revoke it in the dashboard.\n", r.URL, err)
				}
				cancel()
			}
			if cr.MintedTokenId != "" {
				cl := &Client{Remote: r, Token: cr.Token, HTTP: NewHTTPClient()}
				ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
				err := cl.JSON(ctx, http.MethodDelete, "/api/tokens/"+cr.MintedTokenId, nil, nil)
				cancel()
				if err != nil && !IsStatus(err, http.StatusNotFound) && !IsStatus(err, http.StatusUnauthorized) {
					fmt.Fprintf(c.ErrOrStderr(), "Couldn't revoke the API token on %s (%v); revoke it in the dashboard.\n", r.URL, err)
				}
			}
			delete(creds.ByURL, r.URL)
			if err := creds.Save(); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Logged out of %s.\n", r.URL)
			return nil
		},
	}
}
