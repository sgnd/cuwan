package cli

import (
	"fmt"
	"os"

	"github.com/sgnd/cuwan/internal/out"
)

func cmdVersion(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stdout, "usage: cuwan version")
		return 0
	}
	out.Print(os.Stdout, "version", map[string]string{"version": Version}, "cuwan "+Version)
	return 0
}
