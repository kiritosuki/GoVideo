package like

import (
	"context"

	"github.com/kiritosuki/GoVideo/internal/api/video"
	"gorm.io/gorm"
)

type LikeRepo struct {
	db *gorm.DB
}

func NewLikeRepo(db *gorm.DB) *LikeRepo {
	return &LikeRepo{
		db: db,
	}
}

// IsLiked 判断用户是否给视频点过赞
func (r *LikeRepo) IsLiked(ctx context.Context, videoID uint, accountID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&Like{}).
		Where("video_id = ? and account_id = ?", videoID, accountID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListLikedVideos 获取用户已赞的视频列表
func (r *LikeRepo) ListLikedVideos(ctx context.Context, accountID uint) ([]video.Video, error) {
	var videos []video.Video
	err := r.db.WithContext(ctx).
		Model(&video.Video{}).
		Joins("join likes on likes.video_id = videos.id").
		Where("likes.account_id = ?", accountID).
		Order("likes.created_at desc").
		Limit(200).
		Find(&videos).Error
	if err != nil {
		return nil, err
	}
	return videos, nil
}
