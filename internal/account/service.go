package account

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/kiritosuki/GoVideo/internal/auth"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrNewUsernameRequired = errors.New("new_username is required")
	ErrUsernameTaken       = errors.New("username already exists")
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

// Rename 重设用户名
// 根据accountID重设用户名和新的token 并把新的token加入redis缓存 返回新的token
func (s *AccountService) Rename(ctx context.Context, accountID uint, newUsername string) (string, error) {
	if newUsername == "" {
		return "", ErrNewUsernameRequired
	}
	// 生成新的token
	token, err := auth.GenerateToken(accountID, newUsername)
	if err != nil {
		return "", err
	}
	// 重设用户名和token
	if err = s.accountRepo.RenameWithToken(ctx, accountID, newUsername, token); err != nil {
		var mysqlErr *mysql.MySQLError
		// 1062: duplicate entry 用户名已被占用
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return "", ErrUsernameTaken
		}
		// 未找到对应的用户
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
		return "", err
	}
	if s.cache != nil {
		// 把新的token加入redis缓存
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		if err := s.cache.SetBytes(cacheCtx, s.cache.Key("account:%d", accountID), []byte(token), 24*time.Hour); err != nil {
			log.Printf("failed to set cache: %v\n", err)
		}
	}
	return token, nil
}
