package live

import "github.com/sgnd/cuwan/internal/exchangex/binance"

// Native drivers must report order status, or the live loop never refills.
var _ OrderStatuser = (*binance.Driver)(nil)
