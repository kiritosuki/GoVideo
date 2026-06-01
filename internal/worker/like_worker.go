package worker

import (
	"context"
	"encoding/json"
	"errors"
	"go/constant"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

type LikeWorker struct {
	ch        *amqp.Channel
	likeRepo  *like.LikeRepo
	videoRepo *video.VideoRepo
	queue     string
}

func NewLikeWorker(ch *amqp.Channel, likeRepo *like.LikeRepo, videoRepo *video.VideoRepo, queue string) *LikeWorker {
	return &LikeWorker{
		ch:        ch,
		likeRepo:  likeRepo,
		videoRepo: videoRepo,
		queue:     queue,
	}
}

func (w *LikeWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.likeRepo == nil || w.videoRepo == nil {
		return errors.New("like worker is not initialed")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}
	return consumeWithRetry(ctx, w.ch, w.queue, "like_worker", func(ctx context.Context, d amqp.Delivery) error {
		return w.process(ctx, d.Body)
	})
}

// process 回调函数 用于真正消费消息
func (w *LikeWorker) process(ctx context.Context, body []byte) error {
	var event rabbitmq.LikeEvent
	if err := json.Unmarshal(body, &event); err != nil {
		// 解析事件失败 直接丢弃该消息
		return nil
	}
	if event.UserID == 0 || event.VideoID == 0 {
		// 无效消息 直接丢弃
		return nil
	}
	switch event.Action {
	case "like":
		return w.applyLike(ctx, event.UserID, event.VideoID)
	case "unlike":
		return w.applyUnlike(ctx, event.UserID, event.VideoID)
	default:
		// 无效消息 直接丢弃
		return nil
	}
}

// 消费点赞消息
func (w *LikeWorker) applyLike(ctx context.Context, userID uint, videoID uint) error {
	ok, err := w.videoRepo.IsExist(ctx, videoID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	// 插入点赞数据
	created, err := w.likeRepo.LikeIgnoreDuplicate(ctx, &like.Like{
		VideoID:   videoID,
		AccountID: userID,
		CreatedAt: time.Now(),
	})
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	// 更新视频点赞量
	if err := w.videoRepo.ChangeLikesCount(ctx, videoID, 1); err != nil {
		return err
	}
	// 更新视频热度
	return w.videoRepo.ChangePopularity(ctx, videoID, 1)
}

// 消费取消点赞消息
func (w *LikeWorker) applyUnlike(ctx context.Context, userID uint, videoID uint) error {

}
