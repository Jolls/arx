package models

import "time"

type Price struct {
	ID            int
	PartID        int
	SupplierID    *int
	PriceEA       *float64
	PricePack     *float64
	PackSize      *float64
	IsActive      bool
	EffectiveDate *time.Time
	// joined
	SupplierName string
}
