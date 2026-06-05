//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/social"
)

func TestProfileServiceAggregatesCounts(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	owner := env.createAccount(t, "profile-owner")
	follower := env.createAccount(t, "profile-follower")
	vlogger := env.createAccount(t, "profile-vlogger")
	v1 := env.createVideo(t, owner, "profile-video-1", time.Now())
	v2 := env.createVideo(t, owner, "profile-video-2", time.Now())

	if err := env.db.WithContext(ctx).Create(&like.Like{VideoID: v1.ID, AccountID: follower.ID, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("创建点赞1失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Create(&like.Like{VideoID: v2.ID, AccountID: follower.ID, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("创建点赞2失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Model(v1).Update("likes_count", 1).Error; err != nil {
		t.Fatalf("更新v1点赞数失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Model(v2).Update("likes_count", 1).Error; err != nil {
		t.Fatalf("更新v2点赞数失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Create(&social.Social{FollowerID: follower.ID, VloggerID: owner.ID}).Error; err != nil {
		t.Fatalf("创建粉丝关系失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Create(&social.Social{FollowerID: owner.ID, VloggerID: vlogger.ID}).Error; err != nil {
		t.Fatalf("创建关注关系失败: %v", err)
	}

	resp, err := env.profileService().GetAccountProfile(ctx, owner.ID)
	if err != nil {
		t.Fatalf("查询profile失败: %v", err)
	}
	if resp.Account.ID != owner.ID || resp.VideoCount != 2 || resp.TotalLikes != 2 || resp.FollowerCount != 1 || resp.VloggerCount != 1 {
		t.Fatalf("profile聚合结果异常: %+v", resp)
	}
}
