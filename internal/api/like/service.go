package like

import (
	"context"
	"errors"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"github.com/kiritosuki/GoVideo/internal/util"
	"gorm.io/gorm"
)

type LikeService struct {
	likeRepo     *LikeRepo
	videoRepo    *video.VideoRepo
	cache        *rediscache.Client
	likeMQ       *rabbitmq.LikeMQ
	popularityMQ *rabbitmq.PopularityMQ
}

func NewLikeService(likeRepo *LikeRepo, videoRepo *video.VideoRepo, cache *rediscache.Client, likeMQ *rabbitmq.LikeMQ, popularityMQ *rabbitmq.PopularityMQ) *LikeService {
	return &LikeService{
		likeRepo:     likeRepo,
		videoRepo:    videoRepo,
		cache:        cache,
		likeMQ:       likeMQ,
		popularityMQ: popularityMQ,
	}
}

// Like 给视频点赞
func (s *LikeService) Like(ctx context.Context, like *Like) error {
	if like == nil {
		return errors.New("like is nil")
	}
	if like.VideoID == 0 || like.AccountID == 0 {
		return errors.New("video_id and account_id are required")
	}
	// 检查视频是否存在
	if s.videoRepo != nil {
		ok, err := s.videoRepo.IsExist(ctx, like.VideoID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("video not found")
		}
	}
	// 检查该用户是否已经给视频点过赞
	isLiked, err := s.likeRepo.IsLiked(ctx, like.VideoID, like.AccountID)
	if err != nil {
		return err
	}
	if isLiked {
		return errors.New("user has liked this video")
	}

	like.CreatedAt = time.Now()
	mysqlEnqueued := false
	redisEnqueued := false
	// 向消息队列里发送点赞消息
	if s.likeMQ != nil {
		if err := s.likeMQ.Like(ctx, like.AccountID, like.VideoID); err == nil {
			mysqlEnqueued = true
		}
	}
	// 向消息队列里发送视频热度缓存更新消息
	if s.popularityMQ != nil {
		if err := s.popularityMQ.Update(ctx, like.VideoID, 1); err == nil {
			redisEnqueued = true
		}
	}
	// 如果两个消息都发送成功
	if mysqlEnqueued && redisEnqueued {
		return nil
	}
	// fallback 如果向消息队列推送点赞消息失败 直接操作数据库
	if !mysqlEnqueued {
		err := s.likeRepo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// 检查视频是否存在
			if err := tx.Select("id").First(&video.Video{}, like.VideoID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errors.New("video not found")
				}
				return err
			}
			// 插入点赞数据
			if err := tx.Create(like).Error; err != nil {
				if util.IsDupKey(err) {
					return errors.New("user has liked this video")
				}
				return err
			}
			// 更新视频的点赞数量 利用数据库锁的原子更新
			if err := tx.Model(&video.Video{}).Where("id = ?", like.VideoID).
				UpdateColumn("likes_count", gorm.Expr("likes_count + 1")).Error; err != nil {
				return err
			}
			// 更新视频的热度
			if err := tx.Model(&video.Video{}).Where("id = ?", like.VideoID).
				UpdateColumn("popularity", gorm.Expr("popularity + 1")).Error; err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	// fallback 如果向消息队列推送更新视频热度缓存消息失败 直接操作redis
	if !redisEnqueued {
		video.UpdatePopularityCache(ctx, s.cache, like.VideoID, 1)
	}
	return nil
}

// Unlike 给视频取消点赞
func (s *LikeService) Unlike(ctx context.Context, like *Like) error {
	if like == nil {
		return errors.New("like is nil")
	}
	if like.VideoID == 0 || like.AccountID == 0 {
		return errors.New("video_id and account_id are required")
	}
	// 检查视频是否存在
	if s.videoRepo != nil {
		ok, err := s.videoRepo.IsExist(ctx, like.VideoID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("video not found")
		}
	}
	// 检查该用户是否已经给视频点过赞
	isLiked, err := s.likeRepo.IsLiked(ctx, like.VideoID, like.AccountID)
	if err != nil {
		return err
	}
	if !isLiked {
		return errors.New("user has not liked this video")
	}
	mysqlEnqueued := false
	redisEnqueued := false
	// 向消息队列里发送取消点赞消息
	if s.likeMQ != nil {
		if err := s.likeMQ.Unlike(ctx, like.AccountID, like.VideoID); err == nil {
			mysqlEnqueued = true
		}
	}
	// 向消息队列里发送视频热度缓存更新消息
	if s.popularityMQ != nil {
		if err := s.popularityMQ.Update(ctx, like.VideoID, -1); err == nil {
			redisEnqueued = true
		}
	}
	// 如果两个消息都发送成功
	if mysqlEnqueued && redisEnqueued {
		return nil
	}
	// fallback 如果向消息队列推送取消点赞消息失败 直接操作数据库
	if !mysqlEnqueued {
		err := s.likeRepo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// 检查这条点赞记录是否存在 尝试删除
			deleted := tx.Where("video_id = ? and account_id = ?", like.VideoID, like.AccountID).
				Delete(&Like{})
			if deleted.Error != nil {
				return deleted.Error
			}
			// 如果影响行数为0 说明点赞记录不存在
			if deleted.RowsAffected == 0 {
				return errors.New("user has not liked this video")
			}
			// 更新视频的点赞数量 利用数据库锁的原子更新
			if err := tx.Model(&video.Video{}).Where("id = ?", like.VideoID).
				Where("likes_count > 0").
				UpdateColumn("likes_count", gorm.Expr("likes_count - 1")).Error; err != nil {
				return err
			}
			// 更新视频的热度
			if err := tx.Model(&video.Video{}).Where("id = ?", like.VideoID).
				Where("popularity > 0").
				UpdateColumn("popularity", gorm.Expr("popularity - 1")).Error; err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	// fallback 如果向消息队列推送更新视频热度缓存消息失败 直接操作redis
	if !redisEnqueued {
		video.UpdatePopularityCache(ctx, s.cache, like.VideoID, -1)
	}
	return nil
}

// IsLiked 判断用户是否给视频点过赞
func (s *LikeService) IsLiked(ctx context.Context, videoID uint, accountID uint) (bool, error) {
	return s.likeRepo.IsLiked(ctx, videoID, accountID)
}

// ListLikedVideos 获取用户已赞的视频列表
func (s *LikeService) ListLikedVideos(ctx context.Context, accountID uint) ([]video.Video, error) {
	return s.likeRepo.ListLikedVideos(ctx, accountID)
}
