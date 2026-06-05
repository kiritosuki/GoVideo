//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/tag"
	"github.com/kiritosuki/GoVideo/internal/api/video"
)

func TestVideoServicePublishCreatesVideoOutboxAndTags(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "video-author")
	svc := env.videoService()

	v := &video.Video{
		AuthorID:    author.ID,
		Username:    author.Username,
		Title:       " #go 发布集成测试 #go ",
		Description: "这里还有 #redis 标签",
		PlayURL:     "https://example.com/video.mp4",
		CoverURL:    "https://example.com/cover.jpg",
		CreateTime:  time.Now(),
	}
	if err := svc.Publish(ctx, v); err != nil {
		t.Fatalf("发布视频失败: %v", err)
	}
	if v.ID == 0 {
		t.Fatalf("发布后视频ID应该被写回")
	}

	var outbox video.OutboxMsg
	if err := env.db.WithContext(ctx).Where("video_id = ?", v.ID).First(&outbox).Error; err != nil {
		t.Fatalf("发布视频后应写入outbox: %v", err)
	}
	if outbox.Status != video.OutboxStatusPending || outbox.EventType != "video_published" {
		t.Fatalf("outbox初始状态异常: %+v", outbox)
	}

	var tags []tag.Tag
	if err := env.db.WithContext(ctx).Order("name asc").Find(&tags).Error; err != nil {
		t.Fatalf("查询标签失败: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("重复tag应该去重，并提取go/redis两个标签, got=%+v", tags)
	}
}

func TestVideoServiceGetDetailUsesRedisCacheAndFallback(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "video-cache-author")
	v := env.createVideo(t, author, "cache-title-old", time.Now())
	svc := env.videoService()

	first, err := svc.GetDetail(ctx, v.ID)
	if err != nil {
		t.Fatalf("首次查询视频详情失败: %v", err)
	}
	if first.Title != "cache-title-old" {
		t.Fatalf("首次查询应返回DB中的标题, got=%s", first.Title)
	}

	if err := env.db.WithContext(ctx).Model(&video.Video{}).Where("id = ?", v.ID).Update("title", "cache-title-new").Error; err != nil {
		t.Fatalf("更新DB视频标题失败: %v", err)
	}

	// 第二次查询应命中Redis缓存，因此仍返回旧标题。
	second, err := svc.GetDetail(ctx, v.ID)
	if err != nil {
		t.Fatalf("第二次查询视频详情失败: %v", err)
	}
	if second.Title != "cache-title-old" {
		t.Fatalf("第二次查询应命中缓存旧标题, got=%s", second.Title)
	}

	// cache nil 时不使用Redis，直接查DB，验证Redis不可用场景仍能返回结果。
	noCacheSvc := video.NewVideoService(env.repos().video, nil)
	fromDB, err := noCacheSvc.GetDetail(ctx, v.ID)
	if err != nil {
		t.Fatalf("cache nil时查询视频详情失败: %v", err)
	}
	if fromDB.Title != "cache-title-new" {
		t.Fatalf("cache nil时应直接查DB新标题, got=%s", fromDB.Title)
	}
}
