package comment

import (
	"context"
	"errors"
	"strings"

	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/apierror"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"github.com/kiritosuki/GoVideo/internal/util"
	"gorm.io/gorm"
)

type CommentService struct {
	commentRepo  *CommentRepo
	videoRepo    *video.VideoRepo
	cache        *rediscache.Client
	commentMQ    *rabbitmq.CommentMQ
	popularityMQ *rabbitmq.PopularityMQ
}

func NewCommentService(commentRepo *CommentRepo, videoRepo *video.VideoRepo, cache *rediscache.Client, commentMQ *rabbitmq.CommentMQ, popularityMQ *rabbitmq.PopularityMQ) *CommentService {
	return &CommentService{
		commentRepo:  commentRepo,
		videoRepo:    videoRepo,
		cache:        cache,
		commentMQ:    commentMQ,
		popularityMQ: popularityMQ,
	}
}

// Publish 发布评论
func (s *CommentService) Publish(ctx context.Context, comment *Comment) error {
	if comment == nil {
		return errors.New("comment is nil")
	}
	comment.Username = strings.TrimSpace(comment.Username)
	comment.Content = strings.TrimSpace(comment.Content)
	if comment.VideoID == 0 || comment.AuthorID == 0 {
		return errors.New("video_id and author_id are required")
	}
	if comment.Content == "" {
		return errors.New("content is required")
	}
	// 判断视频是否存在
	exists, err := s.videoRepo.IsExist(ctx, comment.VideoID)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("video not found")
	}
	mysqlEnqueued := false
	redisEnqueued := false
	// 向消息队列里发送评论消息
	if s.commentMQ != nil {
		if err := s.commentMQ.Publish(ctx, comment.Username, comment.VideoID, comment.AuthorID, comment.Content); err == nil {
			mysqlEnqueued = true
		}
	}
	// 向消息队列里发送更新视频热度缓存消息
	if s.popularityMQ != nil {
		if err := s.popularityMQ.Update(ctx, comment.VideoID, 1); err == nil {
			redisEnqueued = true
		}
	}
	// 如果两个消息都发送成功
	if mysqlEnqueued && redisEnqueued {
		return nil
	}
	// fallback 如果推送评论消息失败 手动操作mysql
	if !mysqlEnqueued {
		eventID, err := util.RandHex(16)
		if err != nil {
			return err
		}
		comment.EventID = "mock:" + eventID
		if err := s.commentRepo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// 判断视频是否存在
			if err := tx.Select("id").First(&video.Video{}, comment.VideoID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errors.New("video not found")
				}
				return err
			}
			// 向数据库插入评论数据
			if err := tx.Create(comment).Error; err != nil {
				return err
			}
			// 更新视频热度
			if err := tx.Model(&video.Video{}).
				Where("id = ?", comment.VideoID).
				UpdateColumn("popularity", gorm.Expr("popularity + 1")).Error; err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}
	}
	// fallback 如果推送更新视频热度缓存消息失败 手动操作redis
	if !redisEnqueued {
		// 更新视频热度缓存
		video.UpdatePopularityCache(ctx, s.cache, comment.VideoID, 1)
	}
	return nil
}

// Delete 删除评论
func (s *CommentService) Delete(ctx context.Context, commentID uint, accountID uint) error {
	// 查询评论是否存在
	comment, err := s.commentRepo.FindByID(ctx, commentID)
	if err != nil {
		return err
	}
	if comment == nil {
		return errors.New("comment not found")
	}
	// 检验用户身份 是否是评论作者
	if comment.AuthorID != accountID {
		return apierror.ErrUnauthorized
	}
	// 向消息队列里发送删除评论消息
	if s.commentMQ != nil {
		if err := s.commentMQ.Delete(ctx, commentID); err == nil {
			return nil
		}
	}
	// 如果向消息队列发送删除评论消息失败 直接操作mysql
	return s.commentRepo.DeleteComment(ctx, comment)
}

// ListAll 根据视频id列出该视频的所有评论
func (s *CommentService) ListAll(ctx context.Context, videoID uint) ([]Comment, error) {
	// 判断视频是否存在
	exists, err := s.videoRepo.IsExist(ctx, videoID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("video not found")
	}
	// 视频存在 列出所有评论
	return s.commentRepo.ListAllComments(ctx, videoID)
}
