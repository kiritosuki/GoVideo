package video

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/apierror"
	"github.com/kiritosuki/GoVideo/internal/middleware/jwt"
	"github.com/kiritosuki/GoVideo/internal/util"
)

type VideoHandler struct {
	videoService *VideoService
}

func NewVideoHandler(videoService *VideoService) *VideoHandler {
	return &VideoHandler{
		videoService: videoService,
	}
}

// UploadVideo 上传视频 JWT鉴权
// 此接口仅上传视频到服务端存储 返回url链接 不保存数据库
func (h *VideoHandler) UploadVideo(c *gin.Context) {
	authorID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	// 提取前端上传的名为"file"的 multipart/form-data 类型文件
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file"})
		return
	}
	const maxSize = 200 << 20 // 最大限制200MB
	if file.Size <= 0 || file.Size > maxSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file size"})
		return
	}
	// 提取后缀名(小写)
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".mp4" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only .mp4 is allowed"})
		return
	}
	date := time.Now().Format("20060102")
	// 按操作系统规则拼接路径 .run/uploads/videos/2/20060102
	dir := filepath.Join(".run", "uploads", "videos", strconv.FormatUint(uint64(authorID), 10), date)
	// 创建目录 .run/uploads/videos/2/20060102/
	if err = os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 获取随机文件名
	filename, err := util.RandHex(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate filename"})
		return
	}
	filename = filename + ext
	// 拼接绝对路径
	absPath := filepath.Join(dir, filename)
	// 保存到磁盘
	if err = c.SaveUploadedFile(file, absPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 生成视频url链接路径 /static/videos/2/20060102/xxx.mp4
	urlPath := path.Join("/static", "videos", strconv.FormatUint(uint64(authorID), 10), date, filename)
	// 拼接url绝对路径
	videoURL := util.BuildAbsoluteURL(c, urlPath)
	c.JSON(http.StatusOK, UploadVideoResponse{
		URL:     videoURL,
		PlayURL: videoURL,
	})
}

// UploadCover 上传封面 JWT鉴权
// 此接口仅上传封面到服务端存储 返回url链接 不保存数据库
func (h *VideoHandler) UploadCover(c *gin.Context) {
	authorID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
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
	// 提取后缀名(小写)
	ext := strings.ToLower(filepath.Ext(file.Filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "only .jpg/.jpeg/.png/.webp allowed"})
		return
	}
	date := time.Now().Format("20060102")
	// 按操作系统规则拼接路径 .run/uploads/covers/2/20060102
	dir := filepath.Join(".run", "uploads", "covers", strconv.FormatUint(uint64(authorID), 10), date)
	// 创建目录 .run/uploads/covers/2/20060102/
	if err = os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 获取随机文件名
	filename, err := util.RandHex(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate filename"})
		return
	}
	filename = filename + ext
	// 拼接绝对路径
	absPath := filepath.Join(dir, filename)
	// 保存到磁盘
	if err = c.SaveUploadedFile(file, absPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 生成url链接路径 /static/covers/2/20060102/xxx.jpg
	urlPath := path.Join("/static", "covers", strconv.FormatUint(uint64(authorID), 10), date, filename)
	// 拼接url绝对路径
	coverURL := util.BuildAbsoluteURL(c, urlPath)
	c.JSON(http.StatusOK, UploadCoverResponse{
		URL:      coverURL,
		CoverURL: coverURL,
	})
}

// PublishVideo 发布视频 JWT鉴权
func (h *VideoHandler) PublishVideo(c *gin.Context) {
	var req PublishVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	authorID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	username, err := jwt.GetUsername(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	video := &Video{
		AuthorID:    authorID,
		Username:    username,
		Title:       req.Title,
		Description: req.Description,
		PlayURL:     req.PlayURL,
		CoverURL:    req.CoverURL,
		CreateTime:  time.Now(),
	}
	if err = h.videoService.Publish(c.Request.Context(), video); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, video)
}

// ListByAuthorID 根据作者id获取作品列表
func (h *VideoHandler) ListByAuthorID(c *gin.Context) {
	var req ListByAuthorIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	videos, err := h.videoService.ListByAuthorID(c.Request.Context(), req.AuthorID)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if videos == nil {
		videos = []Video{}
	}
	c.JSON(200, videos)
}

// GetDetail 根据id获取视频详细信息
func (h *VideoHandler) GetDetail(c *gin.Context) {
	var req GetDetailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	video, err := h.videoService.GetDetail(c.Request.Context(), req.ID)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, video)
}
