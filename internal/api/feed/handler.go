package feed

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/apierror"
	"github.com/kiritosuki/GoVideo/internal/middleware/jwt"
)

type FeedHandler struct {
	feedService *FeedService
}

func NewFeedHandler(feedService *FeedService) *FeedHandler {
	return &FeedHandler{
		feedService: feedService,
	}
}

// ListLatest 获取最新的几条视频(LatestTime时间点之前) 软鉴权
func (f *FeedHandler) ListLatest(c *gin.Context) {
	var req ListLatestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	// 每次返回视频的数量限制为 [0, 50] 默认每次返回10条
	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}
	var latestTime time.Time
	if req.LatestTime > 0 {
		latestTime = time.UnixMilli(req.LatestTime)
	}
	// 获取当前用户id
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		// 软鉴权不返回error 设置accountID为0
		accountID = 0
	}
	feedItems, err := f.feedService.ListLatest(c.Request.Context(), req.Limit, latestTime, accountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	feedItems.VideoList = nonNilFeedVideoItems(feedItems.VideoList)
	c.JSON(200, feedItems)
}
