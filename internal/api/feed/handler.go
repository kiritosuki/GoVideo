package feed

import (
	"net/http"
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
func (h *FeedHandler) ListLatest(c *gin.Context) {
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
	feedItems, err := h.feedService.ListLatest(c.Request.Context(), req.Limit, latestTime, accountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	feedItems.VideoList = nonNilFeedVideoItems(feedItems.VideoList)
	c.JSON(200, feedItems)
}

// ListLikesCount 获取点赞量最多的几条视频 软鉴权
func (h *FeedHandler) ListLikesCount(c *gin.Context) {
	var req ListLikesCountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	// 每次返回视频的数量限制为 [0, 50] 默认每次返回10条
	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}

	var cursor *LikesCountCursor
	// 总结: likesCountBefore与idBefore必须同时传或者同时不传
	// 同时不传或者同时传0 则cursor为nil
	// 同时传(非双0) 需满足 idBefore != 0 && likesCountBefore >= 0
	if req.LikesCountBefore != nil || req.IDBefore != nil {
		// 请求中likesCountBefore和idBefore两个参数只要传了一个 就必须两格都传
		if req.LikesCountBefore == nil || req.IDBefore == nil {
			c.JSON(400, gin.H{"error": "likes_count_before and id_before must be provided together"})
			return
		}
		// 获取请求中的likesCountBefore和idBefore
		likesCountBefore := *req.LikesCountBefore
		idBefore := *req.IDBefore

		if likesCountBefore < 0 {
			c.JSON(400, gin.H{"error": "invalid cursor: likes_count_before must be >= 0"})
			return
		}
		if idBefore == 0 {
			// 如果idBefore为0 那么likesCountBefore也必须为0
			if likesCountBefore != 0 {
				c.JSON(400, gin.H{"error": "invalid cursor: id_before must be > 0"})
				return
			}
		} else {
			// 若idBefore不为0 创建游标
			cursor = &LikesCountCursor{
				LikesCount: likesCountBefore,
				ID:         idBefore,
			}
		}
	}
	// 获取当前用户id
	viewerAccountID, err := jwt.GetAccountID(c)
	if err != nil {
		// 软鉴权不返回error 设置accountID为0
		viewerAccountID = 0
	}
	feedItems, err := h.feedService.ListLikesCount(c.Request.Context(), req.Limit, cursor, viewerAccountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	feedItems.VideoList = nonNilFeedVideoItems(feedItems.VideoList)
	c.JSON(200, feedItems)
}

// ListByPopularity 获取最热门的几条视频 软鉴权
func (h *FeedHandler) ListByPopularity(c *gin.Context) {
	var req ListByPopularityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	// 每次返回视频的数量限制为 [0, 50] 默认每次返回10条
	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}
	// 获取用户id
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		// 软鉴权不返回error 设置accountID为0
		accountID = 0
	}

	var latestPopularity int64
	var latestBefore time.Time
	var latestIDBefore uint

	if req.LatestPopularity < 0 {
		c.JSON(400, gin.H{"error": "latest_popularity must be >= 0"})
		return
	}
	// LatestBefore 和 LatestIDBefore必须同时传或者同时不传
	anyCursor := !req.LatestBefore.IsZero() || req.LatestIDBefore != nil
	if anyCursor {
		if req.LatestBefore.IsZero() || req.LatestIDBefore == nil || *req.LatestIDBefore == 0 {
			c.JSON(400, gin.H{"error": "latest_before and latest_id_before must be provided together"})
			return
		}
		latestPopularity = req.LatestPopularity
		latestBefore = req.LatestBefore
		latestIDBefore = *req.LatestIDBefore
	}
	resp, err := h.feedService.ListByPopularity(
		c.Request.Context(),
		req.Limit,
		req.AsOf,
		req.Offset,
		accountID,
		latestPopularity,
		latestBefore,
		latestIDBefore,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	resp.VideoList = nonNilFeedVideoItems(resp.VideoList)
	c.JSON(200, resp)
}

// ListByFollowing 获取关注的人的最新视频 JWT鉴权
func (h *FeedHandler) ListByFollowing(c *gin.Context) {
	var req ListByFollowingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	// 每次返回视频的数量限制为 [0, 50] 默认每次返回10条
	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}
	// 获取用户id
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var latestTime time.Time
	if req.LatestTime > 0 {
		latestTime = time.Unix(req.LatestTime, 0)
	}
	feedItems, err := h.feedService.ListByFollowing(c.Request.Context(), req.Limit, latestTime, accountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	feedItems.VideoList = nonNilFeedVideoItems(feedItems.VideoList)
	c.JSON(200, feedItems)
}

// ListByTag 根据标签名称查询视频 按照创建时间降序返回 软鉴权
func (h *FeedHandler) ListByTag(c *gin.Context) {
	var req ListByTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.TagName == "" {
		c.JSON(400, gin.H{"error": "tag_name is required"})
		return
	}
	// 每次返回视频的数量限制为 [0, 50] 默认每次返回10条
	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		// 软鉴权不返回error 设置accountID为0
		accountID = 0
	}
	items, err := h.feedService.ListByTag(c.Request.Context(), req.TagName, req.Limit, accountID)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	resp := ListByTagResponse{
		VideoList: nonNilFeedVideoItems(items),
	}
	c.JSON(200, resp)
}

/* 辅助函数 */

func nonNilFeedVideoItems(items []FeedVideoItem) []FeedVideoItem {
	if items == nil {
		return []FeedVideoItem{}
	}
	return items
}
