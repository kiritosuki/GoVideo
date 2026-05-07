package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	JWTExpire = 15 * time.Minute
)

var randJWTSecret string

// jwtSecret 从环境变量读取jwt签名密钥
// 如果未读取到 则会生成随机的签名密钥(服务一旦重启 随机签名密钥则会失效)
func jwtSecret() []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		if randJWTSecret != "" {
			secret = randJWTSecret
		} else {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				log.Printf("FATAL: cannot generate JWT secret: %v\n", err)
				return []byte("fallback-unsafe-key-change-me")
			}
			secret = hex.EncodeToString(b)
			randJWTSecret = secret
		}
		log.Printf("WARNING: JWT_SECRET not set, generated random key. All tokens invalid on restart.")
	}
	return []byte(secret)
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
	return token.SignedString(jwtSecret())
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
			return jwtSecret(), nil
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
