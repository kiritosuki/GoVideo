package video

import "gorm.io/gorm"

type VideoRepo struct {
	db *gorm.DB
}

func NewVideoRepo(db *gorm.DB) *VideoRepo {
	return &VideoRepo{
		db: db,
	}
}
