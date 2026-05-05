package account

import (
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
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
