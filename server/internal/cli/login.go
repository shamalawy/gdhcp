package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
)

func newLoginCmd(flags *persistentFlags) *cobra.Command {
	var username, password string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against a gdhcp server and save the session token",
		Long: "login calls POST /auth/login with a username and password, then writes the server " +
			"address and the returned session token to the client config file (--config, mode 0600) " +
			"so later commands don't need --server/--token. Username and password are prompted " +
			"interactively when not given as flags; the password prompt does not echo input.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.server == "" {
				return fmt.Errorf("login: --server is required (or set GDHCP_SERVER)")
			}

			in := cmd.InOrStdin()
			out := cmd.OutOrStdout()

			if username == "" {
				var err error
				username, err = promptLine(in, out, "Username: ")
				if err != nil {
					return fmt.Errorf("login: read username: %w", err)
				}
			}
			if password == "" {
				var err error
				password, err = promptPassword(in, out, "Password: ")
				if err != nil {
					return fmt.Errorf("login: read password: %w", err)
				}
			}
			if username == "" || password == "" {
				return fmt.Errorf("login: username and password are required")
			}

			c := client.New(flags.server, "")
			resp, err := c.Login(cmd.Context(), username, password)
			if err != nil {
				return fmt.Errorf("login: %w", err)
			}

			if err := saveClientConfig(flags.configPath, &clientConfig{Server: flags.server, Token: resp.Token}); err != nil {
				return fmt.Errorf("login: save config: %w", err)
			}

			fmt.Fprintf(out, "Logged in to %s as %s. Session expires at %s. Config saved to %s.\n",
				flags.server, username, resp.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"), flags.configPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&username, "username", "", "Username (prompted if omitted)")
	cmd.Flags().StringVar(&password, "password", "", "Password (prompted, without echo, if omitted; prefer the prompt over this flag on shared machines, since flags can leak into shell history/process listings)")

	return cmd
}

// promptLine writes prompt to out and reads one line from in, trimming
// surrounding whitespace.
func promptLine(in interface{ Read([]byte) (int, error) }, out interface{ Write([]byte) (int, error) }, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptPassword writes prompt to out and reads a password from in without
// echoing it, when in is an interactive terminal (os.Stdin); it falls back
// to a plain (echoed) line read otherwise — e.g. when stdin is piped in
// scripted/CI use, where there is no terminal to suppress echo on anyway.
func promptPassword(in interface{ Read([]byte) (int, error) }, out interface{ Write([]byte) (int, error) }, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		data, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(out)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
