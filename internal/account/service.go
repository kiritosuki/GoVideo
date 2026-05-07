package account

import (
	"context"
	"errors"
	"log"
	"strconv"
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

// FindByUsername 根据username查询用户信息
func (s *AccountService) FindByUsername(ctx context.Context, username string) (*Account, error) {
	if account, err := s.accountRepo.FindByUsername(ctx, username); err != nil {
		return nil, err
	} else {
		return account, nil
	}
}

// Login 登录
// 生成token和refreshToken 并更新数据库和redis中的token refreshToken信息
// 返回 token refreshToken error
func (s *AccountService) Login(ctx context.Context, username string, password string) (string, string, error) {
	// 查询用户信息
	account, err := s.FindByUsername(ctx, username)
	if err != nil {
		return "", "", err
	}
	// 验证密码
	if err = bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(password)); err != nil {
		return "", "", err
	}
	// 密码正确 生成token
	token, err := auth.GenerateToken(account.ID, account.Username)
	if err != nil {
		return "", "", err
	}
	// 生成refreshToken
	refreshToken, err := auth.GenerateRefreshToken(account.ID)
	if err != nil {
		return "", "", err
	}
	// 更新用户的token和refreshToken
	if err := s.accountRepo.Login(ctx, account.ID, token, refreshToken); err != nil {
		return "", "", err
	}
	//
	if s.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		if err := s.cache.SetBytes(cacheCtx, s.cache.Key("account:%d", account.ID), []byte(token), 24*time.Hour); err != nil {
			log.Printf("failed to set cache: %v", err)
		}
		if err := s.cache.SetBytes(cacheCtx, s.cache.Key("account:%d:refresh", account.ID), []byte(refreshToken), 7*24*time.Hour); err != nil {
			log.Printf("failed to set refresh cache: %v", err)
		}
		if err := s.cache.SetBytes(cacheCtx, s.cache.Key("refresh:%s", refreshToken), []byte(strconv.FormatUint(uint64(account.ID), 10)), 7*24*time.Hour); err != nil {
			log.Printf("failed to set refresh lookup: %v", err)
		}
	}
	return token, refreshToken, nil
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
