package models

import "time"

type Contact struct {
	ID              int
	CompanyID       *int
	DisplayName     string
	Email           string
	Phone1          string
	Phone2          string
	Fax             string
	Address         string
	City            string
	State           string
	Zipcode         string
	Country         string
	Website         string
	IsActive        bool
	Notes           string
	UpdatedAt       *time.Time
	// joined
	SupplierName string
}
