package feed

import (
	"context"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/video"
	"gorm.io/gorm"
)

type FeedRepo struct {
	db *gorm.DB
}

func NewFeedRepo(db *gorm.DB) *FeedRepo {
	return &FeedRepo{
		db: db,
	}
}

// ListLatest 查询在latestBefore时间点之前的依据create_time desc的前limit条视频
// 如果latestBefore为时间零值 则对所有数据依据create_time desc取前limit条
func (r *FeedRepo) ListLatest(ctx context.Context, limit int, latestBefore time.Time) ([]*video.Video, error) {
	var videos []*video.Video
	query := r.db.WithContext(ctx).
		Model(&video.Video{}).
		Order("create_time desc")
	// 如果latestBefore不为时间零值
	if !latestBefore.IsZero() {
		query = query.Where("create_time < ?", latestBefore)
	}
	// 执行查询操作
	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}
