//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/comment"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

func TestCommentWorkerPublishDeleteAndEventIDIdempotency(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	author := env.createAccount(t, "comment-worker-author")
	user := env.createAccount(t, "comment-worker-user")
	v := env.createVideo(t, author, "comment-worker-video", time.Now())
	rs := env.repos()

	w := worker.NewCommentWorker(env.newChannel(t), rs.comment, rs.video, rabbitmq.CommentQueue)
	go func() {
		_ = w.Run(ctx)
	}()

	evt := rabbitmq.CommentEvent{
		EventID:  "comment-worker-event",
		Action:   "publish",
		Username: user.Username,
		VideoID:  v.ID,
		AuthorID: user.ID,
		Content:  "hello worker",
	}
	if err := env.rmq.PublishJSON(context.Background(), rabbitmq.CommentExchange, rabbitmq.CommentRoutingKeyPublish, evt); err != nil {
		t.Fatalf("发布评论消息失败: %v", err)
	}
	if err := env.rmq.PublishJSON(context.Background(), rabbitmq.CommentExchange, rabbitmq.CommentRoutingKeyPublish, evt); err != nil {
		t.Fatalf("发布重复评论消息失败: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&comment.Comment{}).Where("event_id = ?", evt.EventID).Count(&count)
		var got video.Video
		env.db.First(&got, v.ID)
		return count == 1 && got.Popularity == 1
	})

	var saved comment.Comment
	if err := env.db.Where("event_id = ?", evt.EventID).First(&saved).Error; err != nil {
		t.Fatalf("查询已消费评论失败: %v", err)
	}
	deleteEvt := rabbitmq.CommentEvent{EventID: "comment-delete-event", Action: "delete", CommentID: saved.ID}
	if err := env.rmq.PublishJSON(context.Background(), rabbitmq.CommentExchange, rabbitmq.CommentRoutingKeyDelete, deleteEvt); err != nil {
		t.Fatalf("发布删除评论消息失败: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&comment.Comment{}).Where("id = ?", saved.ID).Count(&count)
		return count == 0
	})
}
