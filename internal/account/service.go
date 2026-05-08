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
	ErrIncorrectPassword   = errors.New("incorrect password")
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
	// 更新用户的token和refreshToken到数据库
	if err := s.accountRepo.UpdateTokenAndRefreshToken(ctx, account.ID, token, refreshToken); err != nil {
		return "", "", err
	}
	//
	if s.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		// 更新redis缓存的token
		if err := s.cache.SetBytes(cacheCtx, s.cache.Key("account:%d", account.ID), []byte(token), 24*time.Hour); err != nil {
			log.Printf("failed to set cache: %v", err)
		}
		// 更新redis缓存的refreshToken 两对kv
		// accountID - refreshToken
		// refreshToken - accountID
		if err := s.cache.SetBytes(cacheCtx, s.cache.Key("account:%d:refresh", account.ID), []byte(refreshToken), 7*24*time.Hour); err != nil {
			log.Printf("failed to set refresh cache: %v", err)
		}
		if err := s.cache.SetBytes(cacheCtx, s.cache.Key("refresh:%s", refreshToken), []byte(strconv.FormatUint(uint64(account.ID), 10)), 7*24*time.Hour); err != nil {
			log.Printf("failed to set refresh lookup: %v", err)
		}
	}
	return token, refreshToken, nil
}

// RefreshToken 刷新token 生成新token替换旧token 续签身份
// 续签token需要用refreshToken做身份验证
// 返回 newToken accountID username error
func (s *AccountService) RefreshToken(ctx context.Context, refreshToken string) (string, uint, string, error) {
	if refreshToken == "" {
		return "", 0, "", errors.New("refresh token is empty")
	}
	// 先根据refreshToken从redis缓存中查询accountID
	if s.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		bytes, err := s.cache.GetBytes(cacheCtx, s.cache.Key("refresh:%s", refreshToken))
		if err == nil {
			// 如果查到了
			idStr := string(bytes)
			id, err := strconv.ParseUint(idStr, 10, 64)
			if err == nil {
				// 根据id从数据库查询用户信息
				account, err := s.FindByID(ctx, uint(id))
				// 如果refreshToken合法
				if err == nil && account != nil && account.RefreshToken == refreshToken {
					// 生成新的token
					newToken, err := auth.GenerateToken(account.ID, account.Username)
					if err != nil {
						return "", 0, "", err
					}
					// 更新数据库中的token
					err = s.accountRepo.UpdateToken(ctx, account.ID, newToken)
					if err != nil {
						return "", 0, "", err
					}
					// 更新redis缓存中的token
					err = s.cache.SetBytes(cacheCtx, s.cache.Key("account:%d", account.ID), []byte(newToken), 24*time.Hour)
					if err != nil {
						return "", 0, "", err
					}
					return newToken, account.ID, account.Username, nil
				}
			}
		}
	}
	// 如果缓存未启用 根据refreshToken从数据库查询用户信息
	account, err := s.FindByRefreshToken(ctx, refreshToken)
	if err != nil {
		return "", 0, "", err
	}
	if account == nil {
		return "", 0, "", errors.New("invalid refresh token")
	}
	// 如果查到了 生成新的token
	newToken, err := auth.GenerateToken(account.ID, account.Username)
	if err != nil {
		return "", 0, "", err
	}
	// 更新数据库中的token
	err = s.accountRepo.UpdateToken(ctx, account.ID, newToken)
	if err != nil {
		return "", 0, "", err
	}
	return newToken, account.ID, account.Username, nil
}

// FindByID 根据id查询用户信息
func (s *AccountService) FindByID(ctx context.Context, id uint) (*Account, error) {
	account, err := s.accountRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return account, nil
}

// FindAll 查询所有用户信息
func (s *AccountService) FindAll(ctx context.Context) ([]*Account, error) {
	return s.accountRepo.FindAll(ctx)
}

// FindByRefreshToken 根据refreshToken查询用户信息
func (s *AccountService) FindByRefreshToken(ctx context.Context, refreshToken string) (*Account, error) {
	return s.accountRepo.FindByRefreshToken(ctx, refreshToken)
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

// ChangePassword 更改密码
func (s *AccountService) ChangePassword(ctx context.Context, username string, oldPassword string, newPassword string) error {
	// 根据username查询用户信息
	account, err := s.FindByUsername(ctx, username)
	if err != nil {
		return err
	}
	// 比较旧密码是否正确
	err = bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(oldPassword))
	if err != nil {
		return ErrIncorrectPassword
	}
	// 更新密码
	bytes, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	newHashPassword := string(bytes)
	err = s.accountRepo.UpdatePassword(ctx, account.ID, newHashPassword)
	if err != nil {
		return err
	}
	return nil
}

// Logout 登出 同时删除token与refreshToken 包括缓存和数据库
func (s *AccountService) Logout(ctx context.Context, accountID uint) error {
	// 根据id查询用户信息
	account, err := s.FindByID(ctx, accountID)
	if err != nil {
		return err
	}
	// 用户token为空 表示用户处于未登录状态 直接返回
	if account.Token == "" {
		return nil
	}
	// 删除缓存中的token
	if s.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		// 删除缓存token
		if err := s.cache.Delete(ctx, s.cache.Key("account:%d", accountID)); err != nil {
			log.Printf("failed to delete token cache: %v\n", err)
		}
		// 删除缓存refreshToken
		if err := s.cache.Delete(ctx, s.cache.Key("account:%d:refresh", accountID)); err != nil {
			log.Printf("failed to delete refreshToken cache: %v\n", err)
		}
		if account.RefreshToken != "" {
			err := s.cache.Delete(cacheCtx, s.cache.Key("refresh:%s", account.RefreshToken))
			if err != nil {
				log.Printf("failed to delete refresh-id cache: %v\n", err)
			}
		}
	}
	// 删除数据库中的token
	return s.accountRepo.DeleteTokenAndRefreshToken(ctx, account.ID)
}

// UpdateAvatar 上传头像
func (s *AccountService) UpdateAvatar(ctx context.Context, accountID uint, avatarURL string) error {
	return s.accountRepo.UpdateAvatar(ctx, accountID, avatarURL)
}
