package apierror

import (
	"errors"
	"net/http"
	"testing"

	"gorm.io/gorm"
)

// 测试业务错误到HTTP状态码的映射
// handler层会统一调用ClassifyHttpStatus 因此这里验证不同错误类型能映射到预期状态码
func TestClassifyHttpStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil error", err: nil, want: http.StatusOK},
		{name: "unauthorized", err: ErrUnauthorized, want: http.StatusUnauthorized},
		{name: "validation", err: ErrValidation, want: http.StatusBadRequest},
		{name: "record not found", err: gorm.ErrRecordNotFound, want: http.StatusNotFound},
		{name: "unknown error", err: errors.New("unknown"), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyHttpStatus(tt.err); got != tt.want {
				t.Fatalf("expected status %d, got %d", tt.want, got)
			}
		})
	}
}
