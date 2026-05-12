package like

import "time"

type Like struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	VideoID   uint      `gorm:"uniqueIndex:idx_likes_video_account,priority:1;not null" json:"video_id"`
	AccountID uint      `gorm:"uniqueIndex:idx_likes_video_account,priority:2;not null" json:"account_id"`
	CreatedAt time.Time `json:"created_at"`
}
