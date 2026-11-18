package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/sgnd/cuwan/internal/home"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

func cmdDoctor(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stdout, "usage: cuwan doctor")
		fmt.Fprintln(os.Stdout, "Checks CUWAN_HOME / ~/.cuwan and the sealed store.")
		return 0
	}
	dir, err := home.Ensure()
	if err != nil {
		return out.Fail("doctor", err, 1)
	}
	storeStatus := "not created yet (run cuwan secret set)"
	keyMode := ""
	if _, err := store.DB(false); err == nil {
		storeStatus = "ok"
		if meta, err := peekMeta(); err == nil {
			keyMode = meta["key_mode"]
		}
	} else if !errors.Is(err, store.ErrNoStore) {
		return out.Fail("doctor", err, 1)
	}
	data := map[string]any{
		"home":     dir,
		"store":    storeStatus,
		"key_mode": keyMode,
		"version":  Version,
	}
	text := fmt.Sprintf("home: %s\nstore: %s\nok", dir, storeStatus)
	if keyMode != "" {
		text = fmt.Sprintf("home: %s\nstore: %s\nkey_mode: %s\nok", dir, storeStatus, keyMode)
	}
	out.Print(os.Stdout, "doctor", data, text)
	return 0
}

func peekMeta() (map[string]string, error) {
	// lazy import cycle avoidance: use sealdb via store path
	return storePeek()
}

func storeKeyMode() (string, error) {
	meta, err := storePeek()
	if err != nil {
		return "", err
	}
	return meta["key_mode"], nil
}
