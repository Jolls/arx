package models

import "time"

type Contact struct {
	CNID              int
	CNSUID            *int
	CNName            string
	CNEmail           string
	CNPhone1          string
	CNPhone2          string
	CNFAX             string
	CNAddress         string
	CNCity            string
	CNState           string
	CNZipcode         string
	CNCountry         string
	CNWeb             string
	CNUserAccountLink string
	CNActive          bool
	CNNotes           string
	CNDateModified    *time.Time
	// joined
	SupplierName string
}
