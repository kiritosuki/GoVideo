package message

import (
	"context"

	"gorm.io/gorm"
)

type MessageRepo struct {
	db *gorm.DB
}

func NewMessageRepo(db *gorm.DB) *MessageRepo {
	return &MessageRepo{
		db: db,
	}
}

// CreateMessage 插入一条消息数据
func (r *MessageRepo) CreateMessage(ctx context.Context, m *Message) error {
	return r.db.WithContext(ctx).
		Create(m).Error
}

// ListMessages 列出消息
func (r *MessageRepo) ListMessages(ctx context.Context, userID uint, peerID uint, limit int) ([]Message, error) {
	var msgs []Message
	err := r.db.WithContext(ctx).
		Where("(from_id = ? and to_id = ?) or (from_id = ? and to_id = ?)", userID, peerID, peerID, userID).
		Order("created_at desc").
		Limit(limit).
		Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	return msgs, nil
}
