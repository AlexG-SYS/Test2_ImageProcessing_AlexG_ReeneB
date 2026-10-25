package data

import "database/sql"

type Models struct {
	Images   ImageModel
	Jobs     JobModel
	Variants VariantModel
}

func NewModels(db *sql.DB) Models {
	return Models{
		Images:   ImageModel{DB: db},
		Jobs:     JobModel{DB: db},
		Variants: VariantModel{DB: db},
	}
}
