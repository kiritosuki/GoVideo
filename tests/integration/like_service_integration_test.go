//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
)

func TestLikeServicePublishesMQWhenAvailable(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "like-service-author")
	user := env.createAccount(t, "like-service-user")
	v := env.createVideo(t, author, "like-service-video", time.Now())
	rs := env.repos()
	mqs := env.mqs(t)
	svc := like.NewLikeService(rs.like, rs.video, env.cache, mqs.like, mqs.popularity)

	if err := svc.Like(ctx, &like.Like{VideoID: v.ID, AccountID: user.ID}); err != nil {
		t.Fatalf("点赞投递MQ失败: %v", err)
	}

	// MQ路径只负责投递消息，最终落库由LikeWorker异步完成。
	if liked, err := rs.like.IsLiked(ctx, v.ID, user.ID); err != nil || liked {
		t.Fatalf("MQ路径下service不应直接写likes表, liked=%v err=%v", liked, err)
	}
	d := consumeOne(t, env.newChannel(t), rabbitmq.LikeQueue, 3*time.Second)
	var evt rabbitmq.LikeEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		t.Fatalf("点赞MQ消息反序列化失败: %v", err)
	}
	if evt.Action != "like" || evt.UserID != user.ID || evt.VideoID != v.ID {
		t.Fatalf("点赞MQ消息内容异常: %+v", evt)
	}
	_ = consumeOne(t, env.newChannel(t), rabbitmq.PopularityQueue, 3*time.Second)
}

func TestLikeServiceFallbackWritesDBAndPopularityCache(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "like-fallback-author")
	user := env.createAccount(t, "like-fallback-user")
	v := env.createVideo(t, author, "like-fallback-video", time.Now())
	rs := env.repos()
	svc := like.NewLikeService(rs.like, rs.video, env.cache, nil, nil)

	if err := svc.Like(ctx, &like.Like{VideoID: v.ID, AccountID: user.ID}); err != nil {
		t.Fatalf("MQ不可用时点赞fallback失败: %v", err)
	}
	if liked, err := rs.like.IsLiked(ctx, v.ID, user.ID); err != nil || !liked {
		t.Fatalf("fallback应直接写likes表, liked=%v err=%v", liked, err)
	}
	var got video.Video
	if err := env.db.WithContext(ctx).First(&got, v.ID).Error; err != nil {
		t.Fatalf("查询视频失败: %v", err)
	}
	if got.LikesCount != 1 || got.Popularity != 1 {
		t.Fatalf("fallback应同步更新点赞数和热度, got likes=%d popularity=%d", got.LikesCount, got.Popularity)
	}

	if err := svc.Unlike(ctx, &like.Like{VideoID: v.ID, AccountID: user.ID}); err != nil {
		t.Fatalf("MQ不可用时取消点赞fallback失败: %v", err)
	}
	if liked, err := rs.like.IsLiked(ctx, v.ID, user.ID); err != nil || liked {
		t.Fatalf("fallback取消点赞应删除likes表记录, liked=%v err=%v", liked, err)
	}
}
