package profile

import "github.com/kiritosuki/GoVideo/internal/api/account"

type GetAccountProfileRequest struct {
	AccountID uint `json:"account_id"`
}

type GetAccountProfileResponse struct {
	Account       account.FindByIDResponse `json:"account"`
	VideoCount    int64                    `json:"video_count"`
	TotalLikes    int64                    `json:"total_likes"`
	FollowerCount int64                    `json:"follower_count"`
	VloggerCount  int64                    `json:"vlogger_count"`
}
