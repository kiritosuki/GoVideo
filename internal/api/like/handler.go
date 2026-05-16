package like

import (
	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/apierror"
	"github.com/kiritosuki/GoVideo/internal/middleware/jwt"
)

type LikeHandler struct {
	likeService *LikeService
}

func NewLikeHandler(likeService *LikeService) *LikeHandler {
	return &LikeHandler{
		likeService: likeService,
	}
}

// Like 给视频点赞 JWT鉴权
func (h *LikeHandler) Like(c *gin.Context) {
	var req LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if req.VideoID <= 0 {
		c.JSON(400, gin.H{"error": "video_id is required"})
		return
	}
	// 获取当前用户id
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	like := &Like{
		VideoID:   req.VideoID,
		AccountID: accountID,
	}
	if err := h.likeService.Like(c.Request.Context(), like); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "like success"})
}

// Unlike 给视频取消点赞 JWT鉴权
func (h *LikeHandler) Unlike(c *gin.Context) {
	var req UnLikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if req.VideoID <= 0 {
		c.JSON(400, gin.H{"error": "video_id is required"})
		return
	}
	// 获取当前用户id
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	like := &Like{
		VideoID:   req.VideoID,
		AccountID: accountID,
	}
	if err := h.likeService.Unlike(c.Request.Context(), like); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "unlike success"})
}

// IsLiked 判断用户是否给视频点过赞 JWT鉴权
func (h *LikeHandler) IsLiked(c *gin.Context) {
	var req IsLikedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if req.VideoID <= 0 {
		c.JSON(400, gin.H{"error": "video_id is required"})
		return
	}
	// 获取当前用户id
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	isLiked, err := h.likeService.IsLiked(c.Request.Context(), req.VideoID, accountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"is_liked": isLiked})
}

// ListMyLikedVideos 获取用户已赞的视频列表 JWT鉴权
func (h *LikeHandler) ListMyLikedVideos(c *gin.Context) {
	// 获取当前用户id
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	// 获取用户已赞的视频列表
	videos, err := h.likeService.ListLikedVideos(c.Request.Context(), accountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if videos == nil {
		videos = []video.Video{}
	}
	c.JSON(200, videos)
}
