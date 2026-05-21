package feed

import (
	"context"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/social"
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

// ListLatest 根据latestBefore游标查询游标之后的视频
// 如果latestBefore为时间零值 则对所有数据查询
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

// GetByID 根据id查询视频
func (r *FeedRepo) GetByID(ctx context.Context, id uint) (*video.Video, error) {
	var v video.Video
	if err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&v).Error; err != nil {
		return nil, err
	}
	return &v, nil
}

// ListLikesCount 根据LikesCountCursor(likesCount + id)游标查询游标之后的视频
// 如果游标为nil 则对所有数据查询
func (r *FeedRepo) ListLikesCount(ctx context.Context, limit int, cursor *LikesCountCursor) ([]*video.Video, error) {
	var videos []*video.Video
	query := r.db.WithContext(ctx).
		Model(&video.Video{}).
		Order("likes_count desc, id desc")
	if cursor != nil {
		query = query.Where("(likes_count < ?) or (likes_count = ? and id < ?)", cursor.LikesCount, cursor.LikesCount, cursor.ID)
	}
	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

// GetByIDs 根据ids查询视频
func (r *FeedRepo) GetByIDs(ctx context.Context, ids []uint) ([]*video.Video, error) {
	var videos []*video.Video
	if len(ids) == 0 {
		return videos, nil
	}
	if err := r.db.WithContext(ctx).
		Model(&video.Video{}).
		Where("id in ?", ids).
		Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

// ListByPopularity 根据 popularity + timeBefore + id 游标查询之后的视频
// 若游标不完整 则查询所有数据
func (r *FeedRepo) ListByPopularity(ctx context.Context, limit int, popularityBefore int64, timeBefore time.Time, idBefore uint) ([]*video.Video, error) {
	var videos []*video.Video
	query := r.db.WithContext(ctx).
		Model(&video.Video{}).
		Order("popularity desc, create_time desc, id desc")
	// 判断游标完整性 popularity可以为零值
	if !timeBefore.IsZero() && idBefore > 0 {
		query = query.Where(
			"(popularity < ?) or (popularity = ? and create_time < ?) or (popularity = ? and create_time = ? and id < ?)",
			popularityBefore,
			popularityBefore, timeBefore,
			popularityBefore, timeBefore, idBefore,
		)
	}
	// 查询数据库
	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

// ListByFollowing 查询关注的人的最新视频 游标：latestBefore
func (r *FeedRepo) ListByFollowing(ctx context.Context, limit int, accountID uint, latestBefore time.Time) ([]*video.Video, error) {
	var videos []*video.Video
	query := r.db.WithContext(ctx).Model(&video.Video{}).Order("create_time desc")
	if accountID > 0 {
		followingQuery := r.db.WithContext(ctx).
			Model(&social.Social{}).
			Select("vlogger_id").
			Where("follower_id = ?", accountID)
		query = query.Where("author_id in (?)", followingQuery)
	}
	if !latestBefore.IsZero() {
		query = query.Where("create_time < ?", latestBefore)
	}
	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}
