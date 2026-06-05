//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

func TestLikeWorkerConsumesLikeUnlikeAndIsIdempotent(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	author := env.createAccount(t, "like-worker-author")
	user := env.createAccount(t, "like-worker-user")
	v := env.createVideo(t, author, "like-worker-video", time.Now())
	rs := env.repos()
	mqs := env.mqs(t)

	w := worker.NewLikeWorker(env.newChannel(t), rs.like, rs.video, rabbitmq.LikeQueue)
	go func() {
		_ = w.Run(ctx)
	}()

	// 连续投递两条点赞消息，唯一索引应保证只产生一次点赞效果。
	if err := mqs.like.Like(context.Background(), user.ID, v.ID); err != nil {
		t.Fatalf("发布点赞消息失败: %v", err)
	}
	if err := mqs.like.Like(context.Background(), user.ID, v.ID); err != nil {
		t.Fatalf("发布重复点赞消息失败: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&like.Like{}).Where("video_id = ? and account_id = ?", v.ID, user.ID).Count(&count)
		var got video.Video
		env.db.First(&got, v.ID)
		return count == 1 && got.LikesCount == 1 && got.Popularity == 1
	})

	if err := mqs.like.Unlike(context.Background(), user.ID, v.ID); err != nil {
		t.Fatalf("发布取消点赞消息失败: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&like.Like{}).Where("video_id = ? and account_id = ?", v.ID, user.ID).Count(&count)
		var got video.Video
		env.db.First(&got, v.ID)
		return count == 0 && got.LikesCount == 0 && got.Popularity == 0
	})
}
