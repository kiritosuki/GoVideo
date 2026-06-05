//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

func TestSocialWorkerFollowUnfollowAndDuplicateFollow(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	follower := env.createAccount(t, "social-worker-follower")
	vlogger := env.createAccount(t, "social-worker-vlogger")
	rs := env.repos()
	mqs := env.mqs(t)

	w := worker.NewSocialWorker(env.newChannel(t), rs.social, rabbitmq.SocialQueue)
	go func() {
		_ = w.Run(ctx)
	}()

	if err := mqs.social.Follow(context.Background(), follower.ID, vlogger.ID); err != nil {
		t.Fatalf("发布关注消息失败: %v", err)
	}
	if err := mqs.social.Follow(context.Background(), follower.ID, vlogger.ID); err != nil {
		t.Fatalf("发布重复关注消息失败: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&social.Social{}).Where("follower_id = ? and vlogger_id = ?", follower.ID, vlogger.ID).Count(&count)
		return count == 1
	})

	if err := mqs.social.Unfollow(context.Background(), follower.ID, vlogger.ID); err != nil {
		t.Fatalf("发布取消关注消息失败: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&social.Social{}).Where("follower_id = ? and vlogger_id = ?", follower.ID, vlogger.ID).Count(&count)
		return count == 0
	})
}
