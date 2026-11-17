// Package out formats CLI results as text for humans or JSON for agents.
package out

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Envelope is the stable agent JSON shape.
type Envelope struct {
	OK      bool           `json:"ok"`
	Command string         `json:"command,omitempty"`
	Data    any            `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
	Hints   []string       `json:"hints,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// Agent reports whether the process should avoid prompts and emit JSON.
func Agent() bool {
	if v := os.Getenv("CUWAN_AGENT"); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) == 0
}

// Print writes a success envelope (JSON) or plain text lines.
func Print(w io.Writer, command string, data any, text string) {
	if Agent() {
		_ = json.NewEncoder(w).Encode(Envelope{OK: true, Command: command, Data: data})
		return
	}
	if text != "" {
		fmt.Fprintln(w, text)
	}
}

// Fail writes an error envelope or stderr message. Returns exit code.
func Fail(command string, err error, code int, hints ...string) int {
	if Agent() {
		_ = json.NewEncoder(os.Stdout).Encode(Envelope{
			OK: false, Command: command, Error: err.Error(), Hints: hints,
		})
		return code
	}
	fmt.Fprintf(os.Stderr, "cuwan: %v\n", err)
	for _, h := range hints {
		fmt.Fprintf(os.Stderr, "hint: %s\n", h)
	}
	return code
}

// Hidden masks a secret value for display.
func Hidden() string { return "[hidden]" }

// SecretRef formats a vault reference for display.
func SecretRef(name string) string { return "[secret:" + name + "]" }
