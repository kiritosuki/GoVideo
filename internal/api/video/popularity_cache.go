package video

import (
	"context"
	"strconv"
	"time"

	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
)

// UpdatePopularityCache 更新视频热度缓存
// 更新指定id的视频在zset中的分钟级热度变化情况 并删除旧的视频缓存
func UpdatePopularityCache(ctx context.Context, cache *rediscache.Client, id uint, change int64) {
	if cache == nil || id == 0 || change == 0 {
		return
	}
	// 删除指定id的视频缓存(点赞/评论等变化 删除旧的缓存)
	cache.Delete(context.Background(), cache.Key("video:detail:id=%d", id))
	// 获取当前时间 截断至颗粒度为分钟 例如 10:30:24 -> 10:30:00
	now := time.Now().UTC().Truncate(time.Minute)
	// 分钟热度级窗口 根据当前时间格式化创建key
	windowKey := cache.Key("hot:video:1m:%s", now.Format("200601021504"))
	// 视频id作为member
	member := strconv.FormatUint(uint64(id), 10)
	cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	// 根据change更改zset排行榜中该视频的score
	cache.ZIncrBy(cacheCtx, windowKey, member, float64(change))
	// 给该zset设置/重置过期时间
	cache.Expire(cacheCtx, windowKey, 2*time.Hour)
}
