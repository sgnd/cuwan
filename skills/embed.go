// Package skills holds the agent skill that ships inside the binary.
package skills

import _ "embed"

// Cuwan is skills/cuwan/SKILL.md. `cuwan guide install` writes it.
//
//go:embed cuwan/SKILL.md
var Cuwan string
