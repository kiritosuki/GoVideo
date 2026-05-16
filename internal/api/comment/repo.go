package comment

import (
	"context"
	"errors"

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
