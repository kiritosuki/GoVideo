package social

import "github.com/kiritosuki/GoVideo/internal/api/account"

type Social struct {
	ID         uint `gorm:"primaryKey"`
	FollowerID uint `gorm:"not null;index;uniqueIndex:idx_socials_follower_vlogger,priority:1"`
	VloggerID  uint `gorm:"not null;index;uniqueIndex:idx_socials_follower_vlogger,priority:2"`
}

type FollowRequest struct {
	VloggerID uint `json:"vlogger_id"`
}

type UnfollowRequest struct {
	VloggerID uint `json:"vlogger_id"`
}

type ListAllFollowersRequest struct {
	VloggerID uint `json:"vlogger_id"`
}

type ListAllFollowersResponse struct {
	Followers     []*account.Account `json:"followers"`
	FollowerCount int64              `json:"follower_count"`
}

type ListAllVloggersRequest struct {
	FollowerID uint `json:"follower_id"`
}

type ListAllVloggersResponse struct {
	Vloggers     []*account.Account `json:"vloggers"`
	VloggerCount int64              `json:"vlogger_count"`
}

type GetCountsResponse struct {
	FollowerCount int64 `json:"follower_count"`
	VloggerCount  int64 `json:"vlogger_count"`
}
