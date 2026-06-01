package message

import (
	"context"
	"errors"
	"strings"
	"time"
)

type MessageService struct {
	messageRepo *MessageRepo
}

func NewMessageService(messageRepo *MessageRepo) *MessageService {
	return &MessageService{
		messageRepo: messageRepo,
	}
}

// Send 发送消息
func (s *MessageService) Send(ctx context.Context, m *Message) error {
	m.Content = strings.TrimSpace(m.Content)
	if m.Content == "" {
		return errors.New("content is required")
	}
	m.CreatedAt = time.Now()
	return s.messageRepo.CreateMessage(ctx, m)
}

// List 列出消息
func (s *MessageService) List(ctx context.Context, userID uint, peerID uint) ([]Message, error) {
	limit := 50
	return s.messageRepo.ListMessages(ctx, userID, peerID, limit)
}
