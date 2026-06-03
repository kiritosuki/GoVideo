package profile

import (
	"github.com/gin-gonic/gin"
)

type ProfileHandler struct {
	profileService *ProfileService
}

func NewProfileHandler(profileService *ProfileService) *ProfileHandler {
	return &ProfileHandler{
		profileService: profileService,
	}
}

// GetAccountProfile 根据id获取用户详细信息(视频数/获赞量/粉丝数/关注数)
func (h *ProfileHandler) GetAccountProfile(c *gin.Context) {
	var req GetAccountProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.AccountID == 0 {
		c.JSON(400, gin.H{"error": "account_id is required"})
		return
	}
	resp, err := h.profileService.GetAccountProfile(c.Request.Context(), req.AccountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, resp)
}
