// Package cli implements the single `gdhcp` binary's command surface
// (docs/AMENDMENTS.md A2): `gdhcp serve` runs the HA DHCP server + API +
// embedded UI, `gdhcp migrate` runs database migrations, and the remaining
// subcommands are a typed REST client against a running server's
// /api/v1 API (docs/api/endpoints.md), authenticated via --token/
// GDHCP_TOKEN/`gdhcp login`.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/version"
)

// persistentFlags holds the root command's persistent flag values, read by
// resolveClient/resolveOutput once cobra has parsed argv.
type persistentFlags struct {
	server     string
	token      string
	output     string
	configPath string
}

// Execute builds and runs the gdhcp root command; server/cmd/gdhcp's main
// simply calls this and exits non-zero on error.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	flags := &persistentFlags{}

	root := &cobra.Command{
		Use:           "gdhcp",
		Short:         "gdhcp — HA DHCP server and IPAM",
		Long:          "gdhcp is a single binary providing an HA-capable DHCP server, an IPAM REST API, an embedded web UI, and this command-line client for that API.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !common.ValidOutputFormat(flags.output) {
				return fmt.Errorf("--output must be one of table|json|yaml, got %q", flags.output)
			}
			return nil
		},
	}

	root.PersistentFlags().StringVar(&flags.server, "server", "", "gdhcp API server address (e.g. http://localhost:8080); overrides GDHCP_SERVER and the config file")
	root.PersistentFlags().StringVar(&flags.token, "token", "", "API bearer token; overrides GDHCP_TOKEN and the config file")
	root.PersistentFlags().StringVar(&flags.output, "output", common.OutputTable, "Output format: table|json|yaml")
	root.PersistentFlags().StringVar(&flags.configPath, "config", defaultClientConfigPath(), "Client config file path (written by 'gdhcp login')")

	// Server-lifecycle commands (own the local machine, not the REST API).
	root.AddCommand(newServeCmd())
	root.AddCommand(newMigrateCmd())
	root.AddCommand(newVersionCmd())
	// `completion` is generated automatically by cobra (bash/zsh/fish/
	// powershell) since root.CompletionOptions.DisableDefaultCmd is left
	// at its zero value (false).
	root.AddCommand(newLoginCmd(flags))

	// API-client resource commands (docs/api/endpoints.md).
	root.AddCommand(newVRFCmd(flags))
	root.AddCommand(newPrefixCmd(flags))
	root.AddCommand(newVLANCmd(flags))
	root.AddCommand(newTreeCmd(flags))
	root.AddCommand(newScopeCmd(flags))
	root.AddCommand(newPoolCmd(flags))
	root.AddCommand(newLeaseCmd(flags))
	root.AddCommand(newTemplateCmd(flags))
	root.AddCommand(newOptionCmd(flags))
	root.AddCommand(newTagCmd(flags))
	root.AddCommand(newClusterCmd(flags))
	root.AddCommand(newUserCmd(flags))
	root.AddCommand(newSearchCmd(flags))
	root.AddCommand(newHACmd(flags))

	return root
}

// resolveClient builds an API client from flags, applying precedence
// --server/--token flag > GDHCP_SERVER/GDHCP_TOKEN env > the client config
// file (written by `gdhcp login`) > a localhost default for the server
// (there is no default token — unauthenticated requests simply get a 401
// from the server, which decodeAPIError renders clearly).
func resolveClient(flags *persistentFlags) (*client.Client, error) {
	server := flags.server
	token := flags.token

	needConfig := server == "" || token == ""
	var cfg *clientConfig
	if needConfig {
		var err error
		cfg, err = loadClientConfig(flags.configPath)
		if err != nil {
			return nil, err
		}
	}

	if server == "" {
		server = os.Getenv("GDHCP_SERVER")
	}
	if server == "" && cfg != nil {
		server = cfg.Server
	}
	if server == "" {
		server = "http://localhost:8080"
	}

	if token == "" {
		token = os.Getenv("GDHCP_TOKEN")
	}
	if token == "" && cfg != nil {
		token = cfg.Token
	}

	return client.New(server, token), nil
}

// runContext bundles what nearly every resource subcommand's RunE needs:
// a request context, an API client, and the active output format.
type runContext struct {
	ctx    context.Context
	client *client.Client
	output string
}

// newRunContext resolves flags into a runContext. Call at the top of every
// RunE — it is cheap (no network I/O) and keeps each command's RunE
// focused on its own request/response handling.
func newRunContext(flags *persistentFlags) (*runContext, error) {
	c, err := resolveClient(flags)
	if err != nil {
		return nil, err
	}
	return &runContext{ctx: context.Background(), client: c, output: flags.output}, nil
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the gdhcp build version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version.Get().String())
			return nil
		},
	}
}
