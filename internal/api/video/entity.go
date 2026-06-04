package video

import "time"

type Video struct {
	ID          uint      `gorm:"primaryKey;index:idx_videos_popularity_time_id,priority:3,sort:desc;index:idx_videos_likes_count_id,priority:2,sort:desc" json:"id"`
	AuthorID    uint      `gorm:"index;not null" json:"author_id"`
	Username    string    `gorm:"type:varchar(255);not null" json:"username"`
	Title       string    `gorm:"type:varchar(255);not null" json:"title"`
	Description string    `gorm:"type:varchar(255);" json:"description,omitempty"`
	PlayURL     string    `gorm:"type:varchar(255);not null" json:"play_url"`
	CoverURL    string    `gorm:"type:varchar(255);not null" json:"cover_url"`
	CreateTime  time.Time `gorm:"autoCreateTime;index:idx_videos_create_time,sort:desc;index:idx_videos_popularity_time_id,priority:2,sort:desc" json:"create_time"`
	LikesCount  int64     `gorm:"column:likes_count;not null;default:0;index:idx_videos_likes_count_id,priority:1,sort:desc" json:"likes_count"`
	Popularity  int64     `gorm:"column:popularity;not null;default:0;index:idx_videos_popularity_time_id,priority:1,sort:desc" json:"popularity"`
}

type OutboxMsg struct {
	ID          uint       `gorm:"primaryKey"`
	VideoID     uint       `gorm:"index"`
	EventType   string     `gorm:"type:varchar(50)"`
	CreateTime  time.Time  `gorm:"autoCreateTime"`
	Status      string     `gorm:"type:varchar(50);index"`
	RetryCount  int        `gorm:"not null;default:0"`
	LastError   string     `gorm:"type:varchar(512)"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime"`
	PublishedAt *time.Time `gorm:"index"`
}

const (
	OutboxStatusPending    = "pending"
	OutboxStatusProcessing = "processing"
	OutboxStatusPublished  = "published"
	OutboxStatusFailed     = "failed"
)

type UploadVideoResponse struct {
	URL     string `json:"url"`
	PlayURL string `json:"play_url"`
}

type UploadCoverResponse struct {
	URL      string `json:"url"`
	CoverURL string `json:"cover_url"`
}

type PublishVideoRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	PlayURL     string `json:"play_url"`
	CoverURL    string `json:"cover_url"`
}

type ListByAuthorIDRequest struct {
	AuthorID uint `json:"author_id"`
}

type GetDetailRequest struct {
	ID uint `json:"id"`
}
