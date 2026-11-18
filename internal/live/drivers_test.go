package live

import (
	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/exchangex/binance"
	"github.com/sgnd/cuwan/internal/exchangex/hyperliquid"
)

// Native drivers must report order status, or the live loop never refills.
var (
	_ OrderStatuser           = (*binance.Driver)(nil)
	_ exchangex.RulesProvider = (*binance.Driver)(nil)
	_ OrderStatuser           = (*hyperliquid.Driver)(nil)
	_ exchangex.RulesProvider = (*hyperliquid.Driver)(nil)
)
