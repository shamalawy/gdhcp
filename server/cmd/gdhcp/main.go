// Command gdhcp is the single gdhcp binary (amendment A2): it provides the
// HA DHCP server, the REST API, the embedded Angular UI, and API-client CLI
// subcommands, all via `gdhcp <subcommand>` (see server/internal/cli).
//
// This file intentionally does nothing but delegate to the CLI package.
// server/internal/cli does not exist yet — it is built by the CLI unit in
// parallel — so `go build ./server/cmd/gdhcp/...` will fail until that
// package lands. That is expected and is why the Foundation unit's build
// verification excludes this package; every other Foundation package
// (models, config, db, store, logger, metrics, version, server) compiles
// independently of it.
package main

import (
	"fmt"
	"os"

	"github.com/shamalawy/gdhcp/server/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
