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

// FindByID 根据id获取账号信息
func (r *AccountRepo) FindByID(ctx context.Context, id uint) (*Account, error) {
	var account Account
	if err := r.db.WithContext(ctx).First(&account, id).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

// CreateAccount 插入一条账号数据
func (r *AccountRepo) CreateAccount(ctx context.Context, account *Account) error {
	if err := r.db.WithContext(ctx).Create(account).Error; err != nil {
		return err
	}
	return nil
}
