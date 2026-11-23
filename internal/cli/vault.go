package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

// secretSet reads the value from stdin only, so it never lands in argv or
// shell history.
func secretSet(args []string) int {
	f, err := parse(args, nil, nil)
	if err != nil {
		return out.Fail("secret.set", err, 2)
	}
	if len(f.pos) != 1 {
		return out.Fail("secret.set", errors.New("usage: printf '%s' VALUE | cuwan secret set NAME (the value is read from stdin, never from the command line)"), 2)
	}
	name := f.pos[0]
	if !out.Agent() && isTerminal() {
		fmt.Fprint(os.Stderr, "value (then Enter): ")
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	val := strings.TrimRight(line, "\r\n")
	if val == "" {
		if err == nil {
			err = errors.New("empty")
		}
		return out.Fail("secret.set", fmt.Errorf("no value on stdin (%v)", err), 2)
	}
	return withEnv("secret.set", func(e *env) int {
		if err := e.st.Update(func(s *store.State) error {
			s.Secrets[name] = val
			return s.Append(map[string]any{"command": "secret.set", "name": name, "agent": out.Agent()})
		}); err != nil {
			return out.Fail("secret.set", err, 1)
		}
		out.Print(os.Stdout, "secret.set", map[string]string{"name": name, "value": out.Hidden()}, name+" = "+out.Hidden())
		return 0
	})
}

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func secretList(args []string) int {
	return withEnv("secret.list", func(e *env) int {
		names := []string{}
		if err := e.st.View(func(s *store.State) error {
			for n := range s.Secrets {
				names = append(names, n)
			}
			return nil
		}); err != nil {
			return out.Fail("secret.list", err, 1)
		}
		sort.Strings(names)
		out.Print(os.Stdout, "secret.list", names, strings.Join(names, "\n"))
		return 0
	})
}

// secretShow says whether a secret exists; the value is never printed.
func secretShow(args []string) int {
	f, err := parse(args, nil, nil)
	if err == nil {
		_, err = needOne(f, "cuwan secret show NAME")
	}
	if err != nil {
		return out.Fail("secret.show", err, 2)
	}
	return withEnv("secret.show", func(e *env) int {
		ok := false
		_ = e.st.View(func(s *store.State) error { _, ok = s.Secrets[f.pos[0]]; return nil })
		if !ok {
			return out.Fail("secret.show", fmt.Errorf("no secret %q", f.pos[0]), 1)
		}
		out.Print(os.Stdout, "secret.show", map[string]string{"name": f.pos[0], "value": out.Hidden()}, f.pos[0]+" = "+out.Hidden())
		return 0
	})
}

func secretRm(args []string) int {
	f, err := parse(args, nil, []string{"yes"})
	if err == nil {
		_, err = needOne(f, "cuwan secret rm NAME [--yes]")
	}
	if err != nil {
		return out.Fail("secret.rm", err, 2)
	}
	if !confirmed(f, "remove secret "+f.pos[0]+"?") {
		return out.Fail("secret.rm", errNeedsPerson, 1)
	}
	return withEnv("secret.rm", func(e *env) int {
		err := e.st.Update(func(s *store.State) error {
			if _, ok := s.Secrets[f.pos[0]]; !ok {
				return fmt.Errorf("no secret %q", f.pos[0])
			}
			for _, x := range s.Exchanges {
				if x.Secret == f.pos[0] {
					return fmt.Errorf("exchange %s uses secret %s", x.Name, f.pos[0])
				}
			}
			delete(s.Secrets, f.pos[0])
			return s.Append(map[string]any{"command": "secret.rm", "name": f.pos[0], "agent": out.Agent()})
		})
		if err != nil {
			return out.Fail("secret.rm", err, 1)
		}
		out.Print(os.Stdout, "secret.rm", map[string]string{"name": f.pos[0]}, "removed "+f.pos[0])
		return 0
	})
}
