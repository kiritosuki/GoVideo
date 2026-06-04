package comment

import "time"

type Comment struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	EventID   string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"event_id"`
	Username  string    `gorm:"index" json:"username"`
	VideoID   uint      `gorm:"index" json:"video_id"`
	AuthorID  uint      `gorm:"index" json:"author_id"`
	Content   string    `gorm:"type:text" json:"content"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

type PublishCommentRequest struct {
	VideoID uint   `json:"video_id"`
	Content string `json:"content"`
}

type DeleteCommentRequest struct {
	CommentID uint `json:"comment_id"`
}

type GetAllCommentsRequest struct {
	VideoID uint `json:"video_id"`
}
