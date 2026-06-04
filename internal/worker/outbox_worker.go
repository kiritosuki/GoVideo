package worker

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"gorm.io/gorm"
)

const (
	defaultOutboxBatchSize         = 100
	defaultOutboxInterval          = time.Second
	defaultOutboxProcessingTimeout = 5 * time.Minute
	maxOutboxRetry                 = 3
	maxOutboxErrorLength           = 512

	OutboxStatusPending    = "pending"
	OutboxStatusProcessing = "processing"
	OutboxStatusPublished  = "published"
	OutboxStatusFailed     = "failed"
)

type OutboxWorker struct {
	db         *gorm.DB
	timelineMQ *rabbitmq.TimelineMQ
	batchSize  int
	interval   time.Duration
	timeout    time.Duration
}

func NewOutboxWorker(db *gorm.DB, timelineMQ *rabbitmq.TimelineMQ) *OutboxWorker {
	return &OutboxWorker{
		db:         db,
		timelineMQ: timelineMQ,
		batchSize:  defaultOutboxBatchSize,
		interval:   defaultOutboxInterval,
		timeout:    defaultOutboxProcessingTimeout,
	}
}

func (w *OutboxWorker) Run(ctx context.Context) error {
	if w == nil || w.db == nil || w.timelineMQ == nil || w.timelineMQ.RabbitMQ == nil || w.timelineMQ.Ch == nil {
		return errors.New("outbox worker is not initialized")
	}
	// 默认每秒扫描100行
	if w.batchSize <= 0 {
		w.batchSize = defaultOutboxBatchSize
	}
	if w.interval <= 0 {
		w.interval = defaultOutboxInterval
	}
	// 默认超时时间为5min
	if w.timeout <= 0 {
		w.timeout = defaultOutboxProcessingTimeout
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		if err := w.pollOnce(ctx); err != nil {
			log.Printf("outbox worker poll failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// pollOnce 进行一次扫描 把这些消息加入到timeline_mq
func (w *OutboxWorker) pollOnce(ctx context.Context) error {
	if err := w.resetExpiredProcessing(ctx); err != nil {
		return err
	}
	var messages []video.OutboxMsg
	if err := w.db.WithContext(ctx).
		Where("status = ?", OutboxStatusPending).
		Order("create_time ASC").
		Limit(w.batchSize).
		Find(&messages).Error; err != nil {
		return err
	}
	for _, msg := range messages {
		if err := w.publishOne(ctx, &msg); err != nil {
			log.Printf("outbox worker publish failed: video_id=%d err=%v", msg.VideoID, err)
		}
	}
	return nil
}

// resetExpiredProcessing 回收超时的processing消息 允许其他节点重新抢占处理
func (w *OutboxWorker) resetExpiredProcessing(ctx context.Context) error {
	deadline := time.Now().Add(-w.timeout)
	return w.db.WithContext(ctx).
		Model(&video.OutboxMsg{}).
		Where("status = ? AND updated_at < ?", OutboxStatusProcessing, deadline).
		Updates(map[string]any{
			"status":     OutboxStatusPending,
			"updated_at": time.Now(),
		}).Error
}

// publishOne 把一条outbox消息加入timeline_mq
// TODO 可选优化: 当前未实现outbox投递消息严格幂等
// TODO 下游timeline使用ZSet天然保证了幂等性 可以容忍低概率的消息被重复投递重复消费
func (w *OutboxWorker) publishOne(ctx context.Context, msg *video.OutboxMsg) error {
	if msg == nil || msg.VideoID == 0 {
		return nil
	}
	// 判断消息状态并更新
	claimed, err := w.claim(ctx, msg.ID)
	if err != nil {
		return err
	}
	// 如果更新失败 说明可能已经被其他节点抢占
	if !claimed {
		// 直接放弃该消息
		return nil
	}
	// 更新成功才算抢占到该条消息
	if err := w.timelineMQ.Publish(ctx, msg.VideoID, msg.CreateTime); err != nil {
		// 投递失败
		if markErr := w.markPublishFailed(ctx, msg, err); markErr != nil {
			log.Printf("outbox worker mark failed status failed: id=%d err=%v", msg.ID, markErr)
		}
		return err
	}
	// 投递成功
	return w.markPublished(ctx, msg.ID)
}

// claim 判断并更新该行数据的状态
// 如果为pending 则更新为processing
// 否则更新失败 返回false
func (w *OutboxWorker) claim(ctx context.Context, id uint) (bool, error) {
	if id == 0 {
		return false, nil
	}
	res := w.db.WithContext(ctx).
		Model(&video.OutboxMsg{}).
		Where("id = ? AND status = ?", id, OutboxStatusPending).
		Updates(map[string]any{
			"status":     OutboxStatusProcessing,
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// markPublishFailed 消息投递成功
func (w *OutboxWorker) markPublished(ctx context.Context, id uint) error {
	if id == 0 {
		return nil
	}
	now := time.Now()
	return w.db.WithContext(ctx).
		Model(&video.OutboxMsg{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":       OutboxStatusPublished,
			"last_error":   "",
			"published_at": &now,
			"updated_at":   now,
		}).Error
}

// markPublishFailed 消息投递失败 有降级重试逻辑
func (w *OutboxWorker) markPublishFailed(ctx context.Context, msg *video.OutboxMsg, publishErr error) error {
	if msg == nil || msg.ID == 0 {
		return nil
	}
	nextRetry := msg.RetryCount + 1
	nextStatus := OutboxStatusPending
	// 重试次数超过最大上限 设置状态为failed
	if nextRetry > maxOutboxRetry {
		nextStatus = OutboxStatusFailed
	}
	return w.db.WithContext(ctx).
		Model(&video.OutboxMsg{}).
		Where("id = ?", msg.ID).
		Updates(map[string]any{
			"status":      nextStatus,
			"retry_count": nextRetry,
			"last_error":  truncateOutboxError(publishErr),
			"updated_at":  time.Now(),
		}).Error
}

func truncateOutboxError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if len(msg) <= maxOutboxErrorLength {
		return msg
	}
	return msg[:maxOutboxErrorLength]
}
