package like

import "gorm.io/gorm"

type LikeRepo struct {
	db *gorm.DB
}

func NewLikeRepo(db *gorm.DB) *LikeRepo {
	return &LikeRepo{
		db: db,
	}
}
