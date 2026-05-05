package account

import (
	"context"

	"gorm.io/gorm"
)

type AccountRepo struct {
	db *gorm.DB
}

func NewAccountRepo(db *gorm.DB) *AccountRepo {
	return &AccountRepo{
		db: db,
	}
}

func (repo *AccountRepo) FindByID(ctx context.Context, id uint) (*Account, error) {
	var account Account
	if err := repo.db.WithContext(ctx).First(&account, id).Error; err != nil {
		return nil, err
	}
	return &account, nil
}
