package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	JWTExpire = 15 * time.Minute
)

var jwtSigningSecret []byte

// SetJWTSecret 设置JWT签名密钥 应用启动后必须先调用
func SetJWTSecret(secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return errors.New("jwt secret is required")
	}
	jwtSigningSecret = []byte(secret)
	return nil
}

func jwtSecret() ([]byte, error) {
	if len(jwtSigningSecret) == 0 {
		return nil, errors.New("jwt secret is not initialized")
	}
	return jwtSigningSecret, nil
}

type Claims struct {
	AccountID uint   `json:"account_id"`
	Username  string `json:"username"`
	jwt.RegisteredClaims
}

// GenerateToken 生成jwt token字符串
func GenerateToken(accountID uint, username string) (string, error) {
	now := time.Now()
	// 获取claims
	claims := Claims{
		AccountID: accountID,
		Username:  username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(JWTExpire)), // 过期时间
			IssuedAt:  jwt.NewNumericDate(now),                // 签发时间
			NotBefore: jwt.NewNumericDate(now),                // 生效时间
		},
	}
	// 生成token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// 签名token
	secret, err := jwtSecret()
	if err != nil {
		return "", err
	}
	return token.SignedString(secret)
}

// GenerateRefreshToken 生成refreshToken
func GenerateRefreshToken(accountID uint) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ParseToken 解析jwt token字符串 返回*Claims
func ParseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenStr,  // token字符串
		&Claims{}, // 指定要解析出的claims类型 需要是指针类型 因为claims中嵌入的RegisteredClaims许多方法实现是指针实现 对应的指针类型才能继承这些实现
		func(token *jwt.Token) (any, error) { //  回调函数 指定签名密钥
			if token.Method == nil || token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, errors.New("unexpected signing method")
			}
			return jwtSecret()
		},
	)
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}
