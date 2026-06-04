package comment

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type CommentRepo struct {
	db *gorm.DB
}

func NewCommentRepo(db *gorm.DB) *CommentRepo {
	return &CommentRepo{
		db: db,
	}
}

// FindByID 根据id查找评论
func (r *CommentRepo) FindByID(ctx context.Context, commentID uint) (*Comment, error) {
	var comment Comment
	if err := r.db.WithContext(ctx).First(&comment, commentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &comment, nil
}

// DeleteComment 删除评论
func (r *CommentRepo) DeleteComment(ctx context.Context, comment *Comment) error {
	return r.db.WithContext(ctx).Delete(comment).Error
}

// ListAllComments 根据视频id列出所有评论
func (r *CommentRepo) ListAllComments(ctx context.Context, videoID uint) ([]Comment, error) {
	var comments []Comment
	if err := r.db.WithContext(ctx).
		Where("video_id = ?", videoID).
		Order("created_at desc").
		Limit(200).
		Find(&comments).Error; err != nil {
		return nil, err
	}
	return comments, nil
}

// CreateComment 创建评论
func (r *CommentRepo) CreateComment(ctx context.Context, comment *Comment) error {
	return r.db.WithContext(ctx).Create(comment).Error
}

// CreateCommentIgnoreDuplicate 创建评论 重复event_id直接忽略
func (r *CommentRepo) CreateCommentIgnoreDuplicate(ctx context.Context, comment *Comment) (bool, error) {
	if comment == nil || comment.EventID == "" {
		return false, nil
	}
	err := r.db.WithContext(ctx).Create(comment).Error
	if err == nil {
		return true, nil
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return false, nil
	}
	return false, err
}

// GetByID 根据id获取评论
func (r *CommentRepo) GetByID(ctx context.Context, id uint) (*Comment, error) {
	var comment Comment
	if err := r.db.WithContext(ctx).First(&comment, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &comment, nil
}
