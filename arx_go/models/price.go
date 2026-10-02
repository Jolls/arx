package models

import (
	"time"

	"github.com/shopspring/decimal"
)

type Price struct {
	ID            int
	PartID        int
	SupplierID    *int
	PriceEA       *decimal.Decimal
	PricePack     *decimal.Decimal
	PackSize      *decimal.Decimal
	IsActive      bool
	EffectiveDate *time.Time
	// joined
	SupplierName string
}
