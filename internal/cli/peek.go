package cli

import (
	"context"

	"github.com/sgnd/cuwan/internal/sealdb"
	"github.com/sgnd/cuwan/internal/store"
)

func storePeek() (map[string]string, error) {
	return sealdb.Peek(context.Background(), store.DBPath())
}
