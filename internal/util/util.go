package util

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

// RandHex 获取指定二进制位数的随机16进制字符串
func RandHex(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// BuildAbsoluteURL 拼接绝对url请求路径
// 如传入 /static/avatars/2/xxx.jpg
// 拼接为 http://example.com/static/avatars/2/xxx.jpg
func BuildAbsoluteURL(c *gin.Context, urlPath string) string {
	// 默认是http 有TLS则是https
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	// 如果用了代理 则检查请求头中的请求协议(一般会放代理之前的请求协议)
	// 例如前端到nginx是https nginx到后端是http 于是nginx会在请求头中放X-Forwarded-Proto为https 便于gin识别
	if xfp := c.GetHeader("X-Forwarded-Proto"); xfp != "" {
		parts := strings.Split(xfp, ",")
		scheme = strings.TrimSpace(parts[0])
	}
	return fmt.Sprintf("%s://%s%s", scheme, c.Request.Host, urlPath)
}
