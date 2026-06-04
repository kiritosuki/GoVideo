package account

import (
	"errors"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/apierror"
	cosstore "github.com/kiritosuki/GoVideo/internal/middleware/cos"
	"github.com/kiritosuki/GoVideo/internal/util"
	"gorm.io/gorm"
)

type AccountHandler struct {
	accountService *AccountService
	cosClient      *cosstore.Client
}

func NewAccountHandler(accountService *AccountService, cosClient *cosstore.Client) *AccountHandler {
	return &AccountHandler{
		accountService: accountService,
		cosClient:      cosClient,
	}
}

// CreateAccount 创建账号
func (h *AccountHandler) CreateAccount(c *gin.Context) {
	var req CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if err := h.accountService.CreateAccount(c.Request.Context(), &Account{
		Username: req.Username,
		Password: req.Password,
	}); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "account created"})
}

// Login 登录
func (h *AccountHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	account, err := h.accountService.FindByUsername(c.Request.Context(), req.Username)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	token, refreshToken, err := h.accountService.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, LoginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		AccountID:    account.ID,
		Username:     account.Username,
	})
}

// Refresh 替换旧token 续签身份 返回新token
func (h *AccountHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	newToken, accountID, username, err := h.accountService.RefreshToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": "invalid refresh token"})
		return
	}
	c.JSON(200, RefreshResponse{
		Token:     newToken,
		AccountID: accountID,
		Username:  username,
	})
}

// Rename 重设用户名 JWT鉴权
func (h *AccountHandler) Rename(c *gin.Context) {
	var req RenameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	accountID, err := getAccountID(c)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	token, err := h.accountService.Rename(c.Request.Context(), accountID, req.NewUsername)
	if err != nil {
		if errors.Is(err, ErrNewUsernameRequired) {
			c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrUsernameTaken) {
			c.JSON(409, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "account not found"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"token": token})
}

// ChangePassword 更改密码
func (h *AccountHandler) ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	err := h.accountService.ChangePassword(c.Request.Context(), req.Username, req.OldPassword, req.NewPassword)
	if err != nil {
		if errors.Is(err, ErrIncorrectPassword) {
			c.JSON(400, gin.H{"error": "incorrect password"})
			return
		}
		c.JSON(500, gin.H{"error": "failed to change password"})
		return
	}
	c.JSON(200, gin.H{"message": "password changed successfully"})
}

// FindByID 根据id查询用户信息
func (h *AccountHandler) FindByID(c *gin.Context) {
	var req FindByIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if account, err := h.accountService.FindByID(c.Request.Context(), req.ID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	} else {
		resp := &FindByIDResponse{
			ID:        account.ID,
			Username:  account.Username,
			AvatarURL: account.AvatarURL,
			Bio:       account.Bio,
		}
		c.JSON(200, resp)
	}
}

// FindByUsername 根据username查询用户信息
func (h *AccountHandler) FindByUsername(c *gin.Context) {
	var req FindByUsernameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if account, err := h.accountService.FindByUsername(c.Request.Context(), req.Username); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	} else {
		c.JSON(200, account)
	}
}

// Logout 登出 JWT鉴权
func (h *AccountHandler) Logout(c *gin.Context) {
	accountID, err := getAccountID(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if err = h.accountService.Logout(c.Request.Context(), accountID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "account logout successfully"})
}

// UploadAvatar 上传头像 JWT鉴权
func (h *AccountHandler) UploadAvatar(c *gin.Context) {
	accountID, err := getAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if h.cosClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cos client is not initialized"})
		return
	}
	// 提取前端上传的名为"file"的 multipart/form-data 类型文件
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file"})
		return
	}
	const maxSize = 10 << 20 // 限制最大10MB
	if file.Size <= 0 || file.Size > maxSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file size"})
		return
	}
	// 获取文件拓展名(小写)
	ext := strings.ToLower(filepath.Ext(file.Filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "only .jpg/.jpeg/.png/.webp allowed"})
		return
	}
	// 临时落盘路径 .run/uploads/avatars/account_id
	dir := filepath.Join(".run", "uploads", "avatars", strconv.FormatUint(uint64(accountID), 10))
	// 创建临时目录 .run/uploads/avatars/account_id/
	if err = os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 生成随机文件名
	filename, err := util.RandHex(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	filename = filename + ext
	// 拼接绝对路径 .run/uploads/avatars/account_id/xxx.jpg
	absPath := filepath.Join(dir, filename)
	// 保存文件到磁盘
	if err = c.SaveUploadedFile(file, absPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 删除临时文件
	defer removeTempFile(absPath)
	// cos对象存储key avatars/2/xxx.jpg
	objectKey := path.Join("avatars", strconv.FormatUint(uint64(accountID), 10), filename)
	// 上传文件到cos
	avatarURL, err := h.cosClient.UploadFile(c.Request.Context(), objectKey, absPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 更新数据库中的avatar_url
	if err = h.accountService.UpdateAvatar(c.Request.Context(), accountID, avatarURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"avatar_url": avatarURL})
}

// UpdateProfile 更新简介 JWT鉴权
func (h *AccountHandler) UpdateProfile(c *gin.Context) {
	// 从上下文获取accountID
	accountID, err := getAccountID(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	var req UpdateProfileRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	// 根据accountID更新简介
	if err = h.accountService.UpdateProfile(c.Request.Context(), accountID, req.AvatarURL, req.Bio); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "profile updated successfully"})
}

/* 未导出函数 */
// getAccountID 从上下文中获取用户ID
func getAccountID(c *gin.Context) (uint, error) {
	value, ok := c.Get("accountID")
	if !ok {
		return 0, errors.New("accountID not found")
	}
	id, ok := value.(uint)
	if !ok {
		return 0, errors.New("accountID has invalid type")
	}
	return id, nil
}

// removeTempFile 删除临时落盘文件
func removeTempFile(filepath string) {
	if err := os.Remove(filepath); err != nil {
		log.Printf("remove temp upload file failed: path=%s err=%v", filepath, err)
	}
}
