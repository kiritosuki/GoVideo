package video

import (
	"context"

	"gorm.io/gorm"
)

type VideoRepo struct {
	db *gorm.DB
}

func NewVideoRepo(db *gorm.DB) *VideoRepo {
	return &VideoRepo{
		db: db,
	}
}

// ListByAuthorID 根据作者id查询视频列表
func (r *VideoRepo) ListByAuthorID(ctx context.Context, authorID uint) ([]Video, error) {
	var videos []Video
	err := r.db.WithContext(ctx).
		Where("author_id = ?", authorID).
		Order("create_time desc").
		Limit(200).
		Find(&videos).Error
	if err != nil {
		return nil, err
	}
	return videos, nil
}

// FindByID 根据id查询视频详细信息
func (r *VideoRepo) FindByID(ctx context.Context, id uint) (*Video, error) {
	var video Video
	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&video).Error
	if err != nil {
		return nil, err
	}
	return &video, nil
}
