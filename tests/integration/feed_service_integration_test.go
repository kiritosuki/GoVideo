//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/feed"
	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	goredis "github.com/redis/go-redis/v9"
)

func TestFeedServiceGetVideosByIDsThreeLevelCacheAndOrder(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "feed-author")
	v1 := env.createVideo(t, author, "feed-video-1", time.Now().Add(-3*time.Minute))
	v2 := env.createVideo(t, author, "feed-video-2", time.Now().Add(-2*time.Minute))
	v3 := env.createVideo(t, author, "feed-video-3", time.Now().Add(-1*time.Minute))
	svc := env.feedService()

	// 首次查询L1/L2都未命中，应从MySQL查询，并按传入ID顺序返回。
	first, err := svc.GetVideosByIDs(ctx, []uint{v3.ID, v1.ID, 999999, v2.ID})
	if err != nil {
		t.Fatalf("首次GetVideosByIDs失败: %v", err)
	}
	if len(first) != 3 || first[0].ID != v3.ID || first[1].ID != v1.ID || first[2].ID != v2.ID {
		t.Fatalf("GetVideosByIDs应保持传入ID顺序并跳过不存在ID, got=%v", idsOf(first))
	}

	// 修改DB和Redis后，原service仍应优先命中L1本地缓存，返回旧标题。
	if err := env.db.WithContext(ctx).Model(&video.Video{}).Where("id = ?", v1.ID).Update("title", "feed-video-1-db-new").Error; err != nil {
		t.Fatalf("更新DB失败: %v", err)
	}
	second, err := svc.GetVideosByIDs(ctx, []uint{v1.ID})
	if err != nil {
		t.Fatalf("第二次GetVideosByIDs失败: %v", err)
	}
	if second[0].Title != "feed-video-1" {
		t.Fatalf("第二次查询应命中L1本地缓存旧值, got=%s", second[0].Title)
	}

	// 新service没有L1缓存；手动写Redis后应走L2，并把结果回填到新service的L1。
	redisCopy := *v2
	redisCopy.Title = "feed-video-2-redis"
	bytes, _ := json.Marshal(redisCopy)
	if err := env.cache.SetBytes(ctx, env.cache.Key("video:entity:%d", v2.ID), bytes, time.Hour); err != nil {
		t.Fatalf("写入Redis视频实体失败: %v", err)
	}
	newSvc := env.feedService()
	fromRedis, err := newSvc.GetVideosByIDs(ctx, []uint{v2.ID})
	if err != nil {
		t.Fatalf("L2查询GetVideosByIDs失败: %v", err)
	}
	if fromRedis[0].Title != "feed-video-2-redis" {
		t.Fatalf("新service应命中Redis缓存, got=%s", fromRedis[0].Title)
	}
}

func TestFeedServiceGetVideosByIDsWorksWithoutRedis(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "feed-no-redis-author")
	v := env.createVideo(t, author, "feed-no-redis-video", time.Now())

	rs := env.repos()
	svc := feed.NewFeedService(rs.feed, rs.like, nil)
	videos, err := svc.GetVideosByIDs(ctx, []uint{v.ID})
	if err != nil {
		t.Fatalf("Redis nil时GetVideosByIDs不应失败: %v", err)
	}
	if len(videos) != 1 || videos[0].ID != v.ID {
		t.Fatalf("Redis nil时应从MySQL返回视频, got=%v", idsOf(videos))
	}
}

func TestFeedServiceListLatestRebuildsTimelineAndBuildsLikedState(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "feed-latest-author")
	viewer := env.createAccount(t, "feed-latest-viewer")
	oldVideo := env.createVideo(t, author, "feed-latest-old", time.Now().Add(-2*time.Hour))
	newVideo := env.createVideo(t, author, "feed-latest-new", time.Now().Add(-time.Hour))
	if err := env.db.WithContext(ctx).Create(&like.Like{VideoID: newVideo.ID, AccountID: viewer.ID, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("创建点赞数据失败: %v", err)
	}

	resp, err := env.feedService().ListLatest(ctx, 2, time.Time{}, viewer.ID)
	if err != nil {
		t.Fatalf("ListLatest失败: %v", err)
	}
	if len(resp.VideoList) != 2 || resp.VideoList[0].ID != newVideo.ID || resp.VideoList[1].ID != oldVideo.ID {
		t.Fatalf("ListLatest应按发布时间倒序返回, got=%+v", resp.VideoList)
	}
	if !resp.VideoList[0].IsLiked {
		t.Fatalf("buildFeedVideos应正确填充当前用户点赞状态")
	}

	// 首次ListLatest会在Redis timeline为空时从MySQL重建ZSET。
	members, err := env.cache.ZRevRange(ctx, env.cache.Key("feed:global_timeline"), 0, -1)
	if err != nil {
		t.Fatalf("查询重建后的timeline失败: %v", err)
	}
	if len(members) != 2 || members[0] != fmt.Sprintf("%d", newVideo.ID) {
		t.Fatalf("timeline重建结果异常: %#v", members)
	}
}

func TestFeedServiceListPopularityAndFollowing(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "feed-pop-author")
	viewer := env.createAccount(t, "feed-pop-viewer")
	v1 := env.createVideo(t, author, "feed-pop-1", time.Now().Add(-3*time.Minute))
	v2 := env.createVideo(t, author, "feed-pop-2", time.Now().Add(-2*time.Minute))
	if err := env.db.WithContext(ctx).Model(&video.Video{}).Where("id = ?", v1.ID).Updates(map[string]any{"popularity": 10, "likes_count": 10}).Error; err != nil {
		t.Fatalf("更新v1热度失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Model(&video.Video{}).Where("id = ?", v2.ID).Updates(map[string]any{"popularity": 30, "likes_count": 30}).Error; err != nil {
		t.Fatalf("更新v2热度失败: %v", err)
	}

	asOf := time.Now().UTC().Truncate(time.Minute)
	windowKey := env.cache.Key("hot:video:1m:%s", asOf.Format("200601021504"))
	if err := env.cache.ZAdd(ctx, windowKey, goredis.Z{Score: 10, Member: fmt.Sprintf("%d", v1.ID)}, goredis.Z{Score: 30, Member: fmt.Sprintf("%d", v2.ID)}); err != nil {
		t.Fatalf("写入分钟热榜失败: %v", err)
	}

	popResp, err := env.feedService().ListByPopularity(ctx, 2, asOf.Unix(), 0, viewer.ID, 0, time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListByPopularity失败: %v", err)
	}
	if len(popResp.VideoList) != 2 || popResp.VideoList[0].ID != v2.ID || popResp.VideoList[1].ID != v1.ID {
		t.Fatalf("热榜应按Redis合并分值倒序返回, got=%+v", popResp.VideoList)
	}

	if err := env.db.WithContext(ctx).Create(&social.Social{FollowerID: viewer.ID, VloggerID: author.ID}).Error; err != nil {
		t.Fatalf("创建关注关系失败: %v", err)
	}
	followingResp, err := env.feedService().ListByFollowing(ctx, 10, time.Time{}, viewer.ID)
	if err != nil {
		t.Fatalf("ListByFollowing失败: %v", err)
	}
	if len(followingResp.VideoList) != 2 {
		t.Fatalf("关注流应返回已关注作者的视频, got=%+v", followingResp.VideoList)
	}
}

func idsOf(videos []*video.Video) []uint {
	ids := make([]uint, 0, len(videos))
	for _, v := range videos {
		ids = append(ids, v.ID)
	}
	return ids
}
