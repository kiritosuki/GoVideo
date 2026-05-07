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

// FindByUsername 根据username查询用户信息
func (r *AccountRepo) FindByUsername(ctx context.Context, username string) (*Account, error) {
	var account Account
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&account).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

// Login 登录 根据accountID更新对应用户的token和refreshToken
func (r *AccountRepo) Login(ctx context.Context, accountID uint, token string, refreshToken string) error {
	err := r.db.WithContext(ctx).
		Model(&Account{}).
		Where("id = ?", accountID).
		Updates(map[string]any{
			"token":         token,
			"refresh_token": refreshToken,
		}).Error
	if err != nil {
		return err
	}
	return nil
}

// RenameWithToken 根据accountID更新用户名和token
func (r *AccountRepo) RenameWithToken(ctx context.Context, accountID uint, newUsername string, token string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		update := tx.Model(&Account{}).Where("id = ?", accountID).Update("username", newUsername)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Model(&Account{}).Where("id = ?", accountID).Update("token", token).Error; err != nil {
			return err
		}
		return nil
	})
}
