package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
	"github.com/sgnd/cuwan/internal/vault"
)

func cmdSecret(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stdout, "usage: cuwan secret set|list|show|rename …")
		return 0
	}
	switch args[0] {
	case "set":
		return secretSet(args[1:])
	case "list":
		return secretList(args[1:])
	case "show":
		return secretShow(args[1:])
	case "rename":
		return secretRename(args[1:])
	default:
		return out.Fail("secret", fmt.Errorf("unknown verb %q", args[0]), 2)
	}
}

func secretSet(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("secret.set", fmt.Errorf("usage: cuwan secret set NAME"), 2)
	}
	val, err := readSecretValue(args)
	if err != nil {
		return out.Fail("secret.set", err, 1)
	}
	if err := vault.Set(pos[0], val); err != nil {
		return out.Fail("secret.set", err, 1)
	}
	_ = store.AppendAudit("secret.set", map[string]any{"name": pos[0]})
	out.Print(os.Stdout, "secret.set", map[string]string{
		"name": pos[0], "ref": "secret:" + pos[0], "value": out.Hidden(),
	}, "saved secret:"+pos[0]+" ("+out.Hidden()+")")
	return 0
}

func secretList(args []string) int {
	list, err := vault.List()
	if err != nil {
		return out.Fail("secret.list", err, 1)
	}
	names := make([]string, 0, len(list))
	for _, i := range list {
		names = append(names, i.Name)
	}
	text := "(none)"
	if len(names) > 0 {
		text = strings.Join(names, "\n")
	}
	out.Print(os.Stdout, "secret.list", list, text)
	return 0
}

func secretShow(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("secret.show", fmt.Errorf("usage: cuwan secret show NAME"), 2)
	}
	if _, err := vault.Get(pos[0]); err != nil {
		return out.Fail("secret.show", err, 1)
	}
	// Never print plaintext to agents; humans need --reveal
	if out.Agent() || !flagBool(args, "--reveal") {
		out.Print(os.Stdout, "secret.show", map[string]string{
			"name": pos[0], "value": out.Hidden(), "ref": out.SecretRef(pos[0]),
		}, out.SecretRef(pos[0])+" = "+out.Hidden())
		return 0
	}
	v, _ := vault.Get(pos[0])
	fmt.Fprintln(os.Stdout, v)
	return 0
}

func secretRename(args []string) int {
	pos := positional(args)
	if len(pos) < 2 {
		return out.Fail("secret.rename", fmt.Errorf("usage: cuwan secret rename OLD NEW"), 2)
	}
	if err := vault.Rename(pos[0], pos[1]); err != nil {
		return out.Fail("secret.rename", err, 1)
	}
	out.Print(os.Stdout, "secret.rename", map[string]string{"from": pos[0], "to": pos[1]}, pos[0]+" → "+pos[1])
	return 0
}
