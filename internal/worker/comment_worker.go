package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/kiritosuki/GoVideo/internal/api/comment"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

type CommentWorker struct {
	ch          *amqp.Channel
	commentRepo *comment.CommentRepo
	videoRepo   *video.VideoRepo
	queue       string
}

func NewCommentWorker(ch *amqp.Channel, commentRepo *comment.CommentRepo, videoRepo *video.VideoRepo, queue string) *CommentWorker {
	return &CommentWorker{
		ch:          ch,
		commentRepo: commentRepo,
		videoRepo:   videoRepo,
		queue:       queue,
	}
}

func (w *CommentWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.commentRepo == nil || w.videoRepo == nil {
		return errors.New("comment worker is not initialized")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}

	return consumeWithRetry(ctx, w.ch, w.queue, "comment_worker", func(ctx context.Context, d amqp.Delivery) error {
		return w.process(ctx, d.Body)
	})
}

// process 回调函数 用于真正消费消息
func (w *CommentWorker) process(ctx context.Context, body []byte) error {
	var evt rabbitmq.CommentEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil
	}
	if evt.VideoID == 0 || evt.AuthorID == 0 {
		return nil
	}
	switch evt.Action {
	case "publish":
		return w.applyPublish(ctx, &evt)
	case "delete":
		return w.applyDelete(ctx, &evt)
	default:
		return nil
	}
}

// applyPublish 消费评论消息
func (w *CommentWorker) applyPublish(ctx context.Context, evt *rabbitmq.CommentEvent) error {
	if evt == nil || evt.EventID == "" || evt.VideoID == 0 || evt.AuthorID == 0 || strings.TrimSpace(evt.Content) == "" {
		return nil
	}
	ok, err := w.videoRepo.IsExist(ctx, evt.VideoID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	c := &comment.Comment{
		EventID:  evt.EventID,
		Username: strings.TrimSpace(evt.Username),
		VideoID:  evt.VideoID,
		AuthorID: evt.AuthorID,
		Content:  strings.TrimSpace(evt.Content),
	}
	// 创建评论
	created, err := w.commentRepo.CreateCommentIgnoreDuplicate(ctx, c)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	// 更新视频热度
	return w.videoRepo.ChangePopularity(ctx, evt.VideoID, 1)
}

// applyDelete 消费删除评论消息
func (w *CommentWorker) applyDelete(ctx context.Context, evt *rabbitmq.CommentEvent) error {
	if evt == nil || evt.CommentID == 0 {
		return nil
	}
	// 根据id获取评论
	c, err := w.commentRepo.GetByID(ctx, evt.CommentID)
	if err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	// 删除评论
	return w.commentRepo.DeleteComment(ctx, c)
}
