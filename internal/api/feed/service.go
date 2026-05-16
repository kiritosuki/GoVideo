package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

type FeedService struct {
	feedRepo     *FeedRepo
	likeRepo     *like.LikeRepo
	redisCache   *rediscache.Client
	localCache   *cache.Cache
	cacheTTL     time.Duration
	requestGroup singleflight.Group
}

func NewFeedService(feedRepo *FeedRepo, likeRepo *like.LikeRepo, redisCache *rediscache.Client) *FeedService {
	return &FeedService{
		feedRepo:   feedRepo,
		likeRepo:   likeRepo,
		redisCache: redisCache,
		localCache: cache.New(3*time.Second, 5*time.Second),
		cacheTTL:   24 * time.Hour,
	}
}

// ListLatest 获取最新的几条视频(LatestTime时间点之前)
func (s *FeedService) ListLatest(ctx context.Context, limit int, latestBefore time.Time, accountID uint) (ListLatestResponse, error) {
	// 获取zset中score最低的一条数据 即create_time最旧的一条视频ID
	zsetTail, err := s.redisCache.ZRangeWithScores(ctx, s.redisCache.Key("feed:global_timeline"), 0, 0)
	if err != nil {
		return ListLatestResponse{}, err
	}
	// 判断redis的zset缓存是否为空
	isZsetEmpty := len(zsetTail) == 0

	// 如果zset为空 让一个线程去查询mysql 构建zset 再重新调用函数递归查询
	if isZsetEmpty {
		// 获取用于singleflight的key
		sfKey := s.redisCache.Key("sf:fallback:global_timeline_rebuild")
		// singleflight机制 只让一个线程去查询mysql 构建zset
		v, err, _ := s.requestGroup.Do(sfKey, func() (interface{}, error) {
			// 无视时间点游标 查询mysql中最新的1000条视频
			videos, err := s.feedRepo.ListLatest(ctx, 1000, time.Time{})
			if err != nil {
				return nil, err
			}
			if len(videos) == 0 {
				return "EMPTY_DB", nil
			}
			// 重建zset
			rebuildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var zElements []redis.Z
			for _, vi := range videos {
				zElements = append(zElements, redis.Z{
					Score:  float64(vi.CreateTime.UnixMilli()),
					Member: fmt.Sprintf("%d", vi.ID),
				})
			}
			s.redisCache.ZAdd(rebuildCtx, s.redisCache.Key("feed:global_timeline"), zElements...)
			return "SUCCESS", nil
		})
		if err != nil {
			return ListLatestResponse{}, err
		}
		if v == "EMPTY_DB" {
			return ListLatestResponse{
				HasMore: false,
			}, nil
		}
		// 让所有请求递归重新查询
		return s.ListLatest(ctx, limit, latestBefore, accountID)
	}

	// 如果zset不为空(或者zset重新构建后的递归查询)
	waterMark := int64(zsetTail[0].Score)
	// 如果latestBefore为时间零值 设置默认值为当前时间
	reqTime := time.Now().UnixMilli()
	if !latestBefore.IsZero() {
		reqTime = latestBefore.UnixMilli()
	}
	var baseVideos []*video.Video

	if reqTime <= waterMark {
		// 冷数据降级查mysql
		sfKey := s.redisCache.Key("sf:cold:listLatest:%d:%d", limit, reqTime)
		v, err, _ := s.requestGroup.Do(sfKey, func() (interface{}, error) {
			return s.feedRepo.ListLatest(ctx, limit, latestBefore)
		})
		if err != nil {
			return ListLatestResponse{}, err
		}
		baseVideos = v.([]*video.Video)
		// 不回写zset 防止冷数据污染热点时间线
	} else {
		// 热数据直接查redis
		maxScore := "+inf" // 正无穷
		if !latestBefore.IsZero() {
			// 如果latestBefore不为时间零值 则修改maxScore为latestBefore
			// 实际做了latestBefore-1 以防相邻两次查询边界重复
			maxScore = fmt.Sprintf("%d", reqTime-1)
		}
		// 查询zset 获取视频ids
		members, err := s.redisCache.ZRevRangeByScore(ctx, s.redisCache.Key("feed:global_timeline"), maxScore, "-inf", 0, int64(limit))
		if err != nil {
			return ListLatestResponse{}, err
		}
		var videoIDs []uint
		for _, member := range members {
			if id, err := strconv.ParseUint(member, 10, 64); err == nil {
				videoIDs = append(videoIDs, uint(id))
			}
		}
		if len(videoIDs) > 0 {
			baseVideos, err = s.GetVideosByIDs(ctx, videoIDs)
			if err != nil {
				return ListLatestResponse{}, err
			}
		}
		// 如果热数据不够一整页 需要拼接冷数据(发生冷热边界击穿)
		if len(baseVideos) < limit {
			remain := limit - len(baseVideos)
			var coldCursor time.Time
			if len(baseVideos) > 0 {
				coldCursor = baseVideos[len(baseVideos)-1].CreateTime
			} else {
				coldCursor = latestBefore
			}
			// singleflight机制 只让一个线程查询mysql中的冷数据
			sfKey := s.redisCache.Key("sf:stitch:listLatest:%d:%d", remain, coldCursor.UnixMilli())
			v, err, _ := s.requestGroup.Do(sfKey, func() (interface{}, error) {
				// 查询数据库中的coldCursor时间之前的前remain条数据
				return s.feedRepo.ListLatest(ctx, remain, coldCursor)
			})
			if err == nil {
				// 拼接冷热数据
				coldVideos := v.([]*video.Video)
				baseVideos = append(baseVideos, coldVideos...)
			}
		}
	}

	var nextTime int64
	if len(baseVideos) > 0 {
		// 将本页最后一条视频的时间作为下一次请求的游标
		nextTime = baseVideos[len(baseVideos)-1].CreateTime.UnixMilli()
	}
	var hasMore bool
	// 判断是否还有下一页
	// 返回false表示一定没有更多内容了 返回true表示大概率有下一页(但也可能刚好查完了)
	hasMore = len(baseVideos) == limit
	// 构造返回切片feedVideos
	feedVideos, err := s.buildFeedVideos(ctx, baseVideos, accountID)
	if err != nil {
		return ListLatestResponse{}, err
	}
	return ListLatestResponse{
		VideoList: feedVideos,
		NextTime:  nextTime,
		HasMore:   hasMore,
	}, nil
}

// GetVideosByIDs 根据 []videoIDs 获取 []video
// 三级架构 L1_本地缓存 -> L2_redis -> L3_mysql
func (s *FeedService) GetVideosByIDs(ctx context.Context, videoIDs []uint) ([]*video.Video, error) {
	if len(videoIDs) == 0 {
		return []*video.Video{}, nil
	}
	videoMap := make(map[uint]*video.Video)

	// L1: 查询本地缓存
	var missedL1 []uint
	for _, id := range videoIDs {
		// 获取本地视频缓存key
		cacheKey := s.redisCache.Key("video:entity:%d", id)
		if s.localCache != nil {
			// 命中本地缓存 加入videoMap
			if v, found := s.localCache.Get(cacheKey); found {
				if data, ok := v.(video.Video); ok {
					videoMap[id] = &data
					continue
				}
			}
		}
		// 本地缓存未命中 记录id
		missedL1 = append(missedL1, id)
	}
	if len(missedL1) == 0 {
		// 本地缓存全部命中 直接返回
		return buildOrderedResult(videoIDs, videoMap), nil
	}

	// L2: 查询redis缓存
	var missedL2 []uint
	if len(missedL1) > 0 {
		cacheKeys := make([]string, len(missedL1))
		for i, id := range missedL1 {
			cacheKeys[i] = s.redisCache.Key("video:entity:%d", id)
		}
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		// 返回的结果len(results) == len(cacheKeys) 索引顺序对应
		results, err := s.redisCache.MGet(cacheCtx, cacheKeys...)
		cancel()
		if err == nil {
			for i, res := range results {
				id := missedL1[i]
				// 如果redis缓存命中
				if res != nil {
					if str, ok := res.(string); ok {
						var v video.Video
						if err := json.Unmarshal([]byte(str), &v); err == nil {
							// 加入结果map
							videoMap[id] = &v
							// 回写更新 L1 本地缓存
							if s.localCache != nil {
								s.localCache.Set(cacheKeys[i], v, 5*time.Second)
							}
							continue
						}
					}
				}
				// 如果redis缓存未命中/类型断言失败/json反序列化失败 则记录id
				missedL2 = append(missedL2, id)
			}
		} else {
			// 如果redis查询出现err 认为redis故障(MGet批量操作查询到不存在不会返回err) 全部降级到L3
			missedL2 = missedL1
			log.Printf("L2 Redis MGet failed, all query to MySQL: %v\n", err)
		}
	}
	if len(missedL2) == 0 {
		return buildOrderedResult(videoIDs, videoMap), nil
	}
	// TODO L3: 查询MySQL
	
}

/* 辅助函数 */

// buildFeedVideos 根据[]video构造[]FeedVideoItem
func (s *FeedService) buildFeedVideos(ctx context.Context, videos []*video.Video, accountID uint) ([]FeedVideoItem, error) {
	feedVideos := make([]FeedVideoItem, len(videos))
	videoIDs := make([]uint, len(videos))
	for i, v := range videos {
		videoIDs[i] = v.ID
	}
	likedMap, err := s.likeRepo.GetBatchLiked(ctx, videoIDs, accountID)
	if err != nil {
		return nil, err
	}
	for i, v := range videos {
		feedVideos[i] = FeedVideoItem{
			ID: v.ID,
			Author: FeedAuthor{
				ID:       v.AuthorID,
				Username: v.Username,
			},
			Title:       v.Title,
			Description: v.Description,
			PlayURL:     v.PlayURL,
			CoverURL:    v.CoverURL,
			CreateTime:  v.CreateTime.Unix(),
			LikesCount:  v.LikesCount,
			IsLiked:     likedMap[v.ID],
		}
	}
	return feedVideos, nil
}

// buildOrderedResult 根据ordererIDs的id顺序和map中的内容构造有序的[]video
func buildOrderedResult(orderedIDs []uint, dataMap map[uint]*video.Video) []*video.Video {
	res := make([]*video.Video, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		if v, ok := dataMap[id]; ok && v != nil {
			res = append(res, v)
		}
	}
	return res
}
