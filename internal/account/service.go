package account

import (
	"context"

	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"golang.org/x/crypto/bcrypt"
)

type AccountService struct {
	accountRepo *AccountRepo
	cache       *rediscache.Client
}

func NewAccountService(accountRepo *AccountRepo, cache *rediscache.Client) *AccountService {
	return &AccountService{
		accountRepo: accountRepo,
		cache:       cache,
	}
}

// CreateAccount 创建账号
func (s *AccountService) CreateAccount(ctx context.Context, account *Account) error {
	// 给密码加密
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(account.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	account.Password = string(passwordHash)
	if err := s.accountRepo.CreateAccount(ctx, account); err != nil {
		return err
	}
	return nil
}
