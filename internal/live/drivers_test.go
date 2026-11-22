package live

import (
	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/exchangex/binance"
	"github.com/sgnd/cuwan/internal/exchangex/hyperliquid"
)

// Both drivers implement the whole interface, order status and rules included.
var (
	_ exchangex.Exchange = (*binance.Driver)(nil)
	_ exchangex.Exchange = (*hyperliquid.Driver)(nil)
)
