package apierror

import (
	"errors"
	"net/http"

	"gorm.io/gorm"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrValidation   = errors.New("validation error")
)

// ClassifyHttpStatus 判断错误类型 返回状态码
// 当err为nil时返回200
func ClassifyHttpStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK // 200
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized // 401
	case errors.Is(err, ErrValidation):
		return http.StatusBadRequest // 400
	case errors.Is(err, gorm.ErrRecordNotFound):
		return http.StatusNotFound // 404
	default:
		return http.StatusInternalServerError // 500
	}
}
