package util

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
)

// 测试RandHex返回指定字节数对应的十六进制字符串长度
// RandHex(16) 应返回32个十六进制字符
func TestRandHexLengthAndFormat(t *testing.T) {
	value, err := RandHex(16)
	if err != nil {
		t.Fatalf("rand hex: %v", err)
	}
	if len(value) != 32 {
		t.Fatalf("expected 32 hex chars, got %d", len(value))
	}
	if ok := regexp.MustCompile(`^[0-9a-f]+$`).MatchString(value); !ok {
		t.Fatalf("expected lower hex string, got %q", value)
	}
}

// 测试连续生成的随机字符串大概率不同
// 这个测试不是证明随机数绝对不重复 只是防止函数返回固定值这类明显错误
func TestRandHexReturnsDifferentValues(t *testing.T) {
	first, err := RandHex(16)
	if err != nil {
		t.Fatalf("first rand hex: %v", err)
	}
	second, err := RandHex(16)
	if err != nil {
		t.Fatalf("second rand hex: %v", err)
	}
	if first == second {
		t.Fatalf("expected different random values, got %q", first)
	}
}

// 测试默认HTTP请求会拼接http协议的绝对URL
// 该函数用于上传本地静态文件时生成可访问URL 这里只验证纯拼接逻辑
func TestBuildAbsoluteURLDefaultHTTP(t *testing.T) {
	c := newTestGinContext("example.com", false, "")

	got := BuildAbsoluteURL(c, "/static/a.jpg")
	want := "http://example.com/static/a.jpg"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// 测试TLS请求会拼接https协议
func TestBuildAbsoluteURLTLS(t *testing.T) {
	c := newTestGinContext("example.com", true, "")

	got := BuildAbsoluteURL(c, "/static/a.jpg")
	want := "https://example.com/static/a.jpg"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// 测试反向代理传入X-Forwarded-Proto时 优先使用代理前的协议
// 例如前端到nginx是https nginx到后端是http 后端仍应返回https链接
func TestBuildAbsoluteURLForwardedProto(t *testing.T) {
	c := newTestGinContext("example.com", false, "https")

	got := BuildAbsoluteURL(c, "/static/a.jpg")
	want := "https://example.com/static/a.jpg"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// 测试MySQL 1062错误能被识别为duplicate key
// 项目中点赞/关注/通知幂等逻辑依赖这个判断
func TestIsDupKey(t *testing.T) {
	err := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}

	if !IsDupKey(err) {
		t.Fatal("expected mysql 1062 error to be duplicate key")
	}
}

func newTestGinContext(host string, tlsEnabled bool, forwardedProto string) *gin.Context {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
	if tlsEnabled {
		req = httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil)
	}
	if forwardedProto != "" {
		req.Header.Set("X-Forwarded-Proto", forwardedProto)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}
