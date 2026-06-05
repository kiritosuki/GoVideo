//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
)

func TestSocialServicePublishesMQWhenAvailable(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	follower := env.createAccount(t, "social-service-follower")
	vlogger := env.createAccount(t, "social-service-vlogger")
	rs := env.repos()
	mqs := env.mqs(t)
	svc := social.NewSocialService(rs.social, rs.account, mqs.social)

	if err := svc.Follow(ctx, &social.Social{FollowerID: follower.ID, VloggerID: vlogger.ID}); err != nil {
		t.Fatalf("关注投递MQ失败: %v", err)
	}
	if followed, err := rs.social.IsFollowed(ctx, &social.Social{FollowerID: follower.ID, VloggerID: vlogger.ID}); err != nil || followed {
		t.Fatalf("MQ路径下service不应直接写socials表, followed=%v err=%v", followed, err)
	}

	d := consumeOne(t, env.newChannel(t), rabbitmq.SocialQueue, 3*time.Second)
	var evt rabbitmq.SocialEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		t.Fatalf("关注MQ消息反序列化失败: %v", err)
	}
	if evt.Action != "follow" || evt.FollowerID != follower.ID || evt.VloggerID != vlogger.ID {
		t.Fatalf("关注MQ消息内容异常: %+v", evt)
	}
}

func TestSocialServiceFallbackAndValidation(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	follower := env.createAccount(t, "social-fallback-follower")
	vlogger := env.createAccount(t, "social-fallback-vlogger")
	rs := env.repos()
	svc := social.NewSocialService(rs.social, rs.account, nil)

	if err := svc.Follow(ctx, &social.Social{FollowerID: follower.ID, VloggerID: follower.ID}); err == nil {
		t.Fatalf("不能关注自己")
	}
	if err := svc.Follow(ctx, &social.Social{FollowerID: follower.ID, VloggerID: vlogger.ID}); err != nil {
		t.Fatalf("MQ不可用时关注fallback失败: %v", err)
	}
	if followed, err := rs.social.IsFollowed(ctx, &social.Social{FollowerID: follower.ID, VloggerID: vlogger.ID}); err != nil || !followed {
		t.Fatalf("fallback应直接写socials表, followed=%v err=%v", followed, err)
	}
	if err := svc.Follow(ctx, &social.Social{FollowerID: follower.ID, VloggerID: vlogger.ID}); err == nil {
		t.Fatalf("重复关注应该被service拒绝")
	}
	if err := svc.Unfollow(ctx, &social.Social{FollowerID: follower.ID, VloggerID: vlogger.ID}); err != nil {
		t.Fatalf("MQ不可用时取消关注fallback失败: %v", err)
	}
	if followed, err := rs.social.IsFollowed(ctx, &social.Social{FollowerID: follower.ID, VloggerID: vlogger.ID}); err != nil || followed {
		t.Fatalf("fallback取消关注应删除socials表记录, followed=%v err=%v", followed, err)
	}
}
