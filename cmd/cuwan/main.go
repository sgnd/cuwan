// Command cuwan runs local-first CEX/DEX grid bots.
// For people and AI agents. State lives under ~/.cuwan.
package main

import (
	"os"

	"github.com/sgnd/cuwan/internal/cli"
	"github.com/sgnd/cuwan/internal/vault"
)

func main() {
	vault.TakeUnlockEnv()
	os.Exit(cli.Execute())
}
