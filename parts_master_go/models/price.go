package models

type Price struct {
	ID           int
	PartID       int
	SupplierID   *int
	PriceEA      *float64
	PricePack    *float64
	PackSize     *float64
	IsActive     bool
	// joined
	SupplierName string
}
