package social

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/apierror"
	"github.com/kiritosuki/GoVideo/internal/middleware/jwt"
)

type SocialHandler struct {
	socialService *SocialService
}

func NewSocialHandler(socialService *SocialService) *SocialHandler {
	return &SocialHandler{
		socialService: socialService,
	}
}

// Follow 关注
func (h *SocialHandler) Follow(c *gin.Context) {
	var req FollowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if req.VloggerID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vlogger_id is required"})
		return
	}
	// 获取当前用户id
	FollowerID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	social := &Social{
		FollowerID: FollowerID,
		VloggerID:  req.VloggerID,
	}
	if err := h.socialService.Follow(c.Request.Context(), social); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "followed successfully"})
}

// Unfollow 取消关注
func (h *SocialHandler) Unfollow(c *gin.Context) {
	var req UnfollowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if req.VloggerID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vlogger_id is required"})
		return
	}
	// 获取当前用户id
	FollowerID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	social := &Social{
		FollowerID: FollowerID,
		VloggerID:  req.VloggerID,
	}
	if err := h.socialService.Unfollow(c.Request.Context(), social); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "unfollowed successfully"})
}

// ListAllFollowers 列出所有粉丝
func (h *SocialHandler) ListAllFollowers(c *gin.Context) {
	var req ListAllFollowersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	// 获取请求中的vloggerID
	vloggerID := req.VloggerID
	// 如果vloggerID不存在 就获取当前用户id
	if vloggerID == 0 {
		accountID, err := jwt.GetAccountID(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		vloggerID = accountID
	}
	// 列出所有粉丝
	followers, err := h.socialService.ListAllFollowers(c.Request.Context(), vloggerID)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if followers == nil {
		followers = []*account.Account{}
	}
	// 获取粉丝数
	followerCount, _ := h.socialService.CountFollowers(c.Request.Context(), vloggerID)
	c.JSON(http.StatusOK, ListAllFollowersResponse{
		Followers:     followers,
		FollowerCount: followerCount,
	})
}

// ListAllVloggers 列出所有关注的人
func (h *SocialHandler) ListAllVloggers(c *gin.Context) {
	var req ListAllVloggersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	// 获取请求中的followerID
	followerID := req.FollowerID
	// 若followerID为空 则获取当前用户id
	if followerID == 0 {
		accountID, err := jwt.GetAccountID(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		followerID = accountID
	}
	// 列出所有关注的人
	vloggers, err := h.socialService.ListAllVloggers(c.Request.Context(), followerID)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if vloggers == nil {
		vloggers = []*account.Account{}
	}
	// 获取关注数
	vloggerCount, _ := h.socialService.CountVloggers(c.Request.Context(), followerID)
	c.JSON(http.StatusOK, ListAllVloggersResponse{
		Vloggers:     vloggers,
		VloggerCount: vloggerCount,
	})
}

// GetCounts 获取当前用户的关注数和粉丝数
func (h *SocialHandler) GetCounts(c *gin.Context) {
	// 获取当前用户id
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	// 获取粉丝数
	followerCount, _ := h.socialService.CountFollowers(c.Request.Context(), accountID)
	// 获取关注数
	vloggerCount, _ := h.socialService.CountVloggers(c.Request.Context(), accountID)
	c.JSON(http.StatusOK, GetCountsResponse{
		FollowerCount: followerCount,
		VloggerCount:  vloggerCount,
	})
}
