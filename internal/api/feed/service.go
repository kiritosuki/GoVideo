package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
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
	// L3: 查询MySQL
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, id := range missedL2 {
		wg.Add(1)
		// 在for循环内部的协程中直接使用外面的id是危险操作 需要传入副本id参数
		// 外面的id会随着for循环的不断进行而变化 影响到协程内的id 而协程何时被调度是不确定的
		go func(videoID uint) {
			defer wg.Done()
			sfKey := s.redisCache.Key("sf:entity:%d", videoID)
			v, err, _ := s.requestGroup.Do(sfKey, func() (interface{}, error) {
				v, err := s.feedRepo.GetByID(ctx, videoID)
				if err != nil || v == nil {
					return nil, err
				}
				safeCopy := *v
				cacheKey := s.redisCache.Key("video:entity:%d", v.ID)
				if bytes, err := json.Marshal(safeCopy); err == nil {
					// 查询到视频 则异步回写redis
					go func(key string, b []byte) {
						cacheCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
						defer cancel()
						s.redisCache.SetBytes(cacheCtx, key, b, time.Hour)
					}(cacheKey, bytes)
				}
				return v, err
			})
			// 如果成功查询到了 加入结果map
			if err == nil && v != nil {
				safeCopy := *(v.(*video.Video))
				mu.Lock()
				videoMap[videoID] = &safeCopy
				mu.Unlock()
				// 加入本地缓存
				s.localCache.Set(s.redisCache.Key("video:entity:%d", safeCopy.ID), safeCopy, 5*time.Second)
			}
		}(id)
	}
	wg.Wait()
	return buildOrderedResult(videoIDs, videoMap), nil
}

// ListLatest 获取最新的几条视频 游标为latestBefore
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

// ListLikesCount 获取点赞数最多的前几条视频 游标为LikesCountCursor(likesCount + id)
func (s *FeedService) ListLikesCount(ctx context.Context, limit int, cursor *LikesCountCursor, accountID uint) (ListLikesCountResponse, error) {
	videos, err := s.feedRepo.ListLikesCount(ctx, limit, cursor)
	if err != nil {
		return ListLikesCountResponse{}, err
	}
	// 返回false表示一定没有更多内容了 返回true表示大概率有下一页(但也可能刚好查完了)
	hasMore := len(videos) == limit
	feedVideoItems, err := s.buildFeedVideos(ctx, videos, accountID)
	if err != nil {
		return ListLikesCountResponse{}, err
	}
	res := ListLikesCountResponse{
		VideoList: feedVideoItems,
		HasMore:   hasMore,
	}
	if len(videos) > 0 {
		lastVideo := videos[len(videos)-1]
		nextID := lastVideo.ID
		nextLikesCount := lastVideo.LikesCount
		res.NextIDBefore = &nextID
		res.NextLikesCountBefore = &nextLikesCount
	}
	return res, nil
}

// ListByPopularity 获取最热门的前几条视频 分钟级热榜合并 分页查询
func (s *FeedService) ListByPopularity(ctx context.Context, limit int, reqAsOf int64, offset int, accountID uint, latestPopularity int64, latestBefore time.Time, latestIDBefore uint) (ListByPopularityResponse, error) {
	// 如果运行了redis
	if s.redisCache != nil {
		// 从参数获取AsOf时间 精度截止到分钟 参数为0值则设置为当前时间
		asOf := time.Now().UTC().Truncate(time.Minute)
		if reqAsOf > 0 {
			asOf = time.Unix(reqAsOf, 0).UTC().Truncate(time.Minute)
		}
		// 窗口大小为60min
		const win = 60
		keys := make([]string, 0, win)
		for i := 0; i < win; i++ {
			// 从asOf时间节点开始 获取过去一小时里的分钟级热度窗口的key
			keys = append(keys, s.redisCache.Key("hot:video:1m:%s", asOf.Add(-time.Duration(i)*time.Minute).Format("200601021504")))
		}
		// 合并后的小时级热度窗口的key
		dest := s.redisCache.Key("hot:video:merge:1m:%s", asOf.Format("200601021504"))
		opCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		defer cancel()
		// 判断小时级热度窗口缓存是否存在
		exists, _ := s.redisCache.Exists(opCtx, dest)
		if !exists {
			// 不存在则创建 对这60个分钟级热度缓存取并集 过期时间为2min
			s.redisCache.ZUnionStore(opCtx, dest, keys, "SUM")
			s.redisCache.Expire(opCtx, dest, 2*time.Minute)
		}
		// 页起始与结束索引
		start := int64(offset)
		stop := start + int64(limit) - 1
		// 查询members
		members, err := s.redisCache.ZRevRange(opCtx, dest, start, stop)
		if err == nil && len(members) == 0 {
			// 如果查询的起始索引大于0但没有数据 说明查完了
			if offset > 0 {
				return ListByPopularityResponse{
					VideoList:  []FeedVideoItem{},
					AsOf:       asOf.Unix(),
					NextOffset: offset,
					HasMore:    false,
				}, nil
			}
		}
		// 如果正常查询到了结果(member为视频id)
		if err == nil && len(members) > 0 {
			ids := make([]uint, 0, len(members))
			// 收集到ids
			for _, m := range members {
				id, err := strconv.ParseUint(m, 10, 64)
				if err == nil && id > 0 {
					ids = append(ids, uint(id))
				}
			}
			// 根据ids查询videos
			videos, err := s.feedRepo.GetByIDs(ctx, ids)
			if err == nil {
				// 把videos按照ids的顺序排序存入[]ordered
				vMap := make(map[uint]*video.Video, len(videos))
				for _, v := range videos {
					vMap[v.ID] = v
				}
				ordered := make([]*video.Video, 0, len(ids))
				for _, i := range ids {
					if v := vMap[i]; v != nil {
						ordered = append(ordered, v)
					}
				}
				// 构建返回对象
				items, err := s.buildFeedVideos(ctx, ordered, accountID)
				if err != nil {
					return ListByPopularityResponse{}, err
				}
				resp := ListByPopularityResponse{
					VideoList:  items,
					AsOf:       asOf.Unix(),
					NextOffset: offset + len(items),
					HasMore:    len(items) == limit,
				}
				if len(ordered) > 0 {
					last := ordered[len(ordered)-1]
					nextPopularity := last.Popularity
					nextBefore := last.CreateTime
					nextID := last.ID
					resp.NextLatestPopularity = &nextPopularity
					resp.NextLatestBefore = &nextBefore
					resp.NextLatestIDBefore = &nextID
				}
				return resp, nil
			}
		}
	}
	// 如果redis故障 降级查数据库
	videos, err := s.feedRepo.ListByPopularity(ctx, limit, latestPopularity, latestBefore, latestIDBefore)
	if err != nil {
		return ListByPopularityResponse{}, err
	}
	items, err := s.buildFeedVideos(ctx, videos, accountID)
	if err != nil {
		return ListByPopularityResponse{}, err
	}
	// AsOf和NextOffset返回0 使下次请求能先尝试redis并刷新时间
	// 如果redis宕机修复 此机制可以自愈
	resp := ListByPopularityResponse{
		VideoList:  items,
		AsOf:       0,
		NextOffset: 0,
		HasMore:    len(items) == limit,
	}
	// redis可能仍宕机 需要用传下次查询mysql的游标
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		nextPopularity := last.Popularity
		nextBefore := last.CreateTime
		nextID := last.ID
		resp.NextLatestPopularity = &nextPopularity
		resp.NextLatestBefore = &nextBefore
		resp.NextLatestIDBefore = &nextID
	}
	return resp, nil
}

// ListByFollowing 获取关注的人的最新视频 以latestBefore作为游标
func (s *FeedService) ListByFollowing(ctx context.Context, limit int, latestBefore time.Time, accountID uint) (ListByFollowingResponse, error) {
	// 用于查询数据库的函数
	doListByFollowingFromDB := func() (ListByFollowingResponse, error) {
		// 查询关注的人的最新视频
		videos, err := s.feedRepo.ListByFollowing(ctx, limit, accountID, latestBefore)
		if err != nil {
			return ListByFollowingResponse{}, err
		}
		var nextTime int64
		if len(videos) > 0 {
			nextTime = videos[len(videos)-1].CreateTime.Unix()
		} else {
			nextTime = 0
		}
		hasMore := len(videos) == limit
		feedVideos, err := s.buildFeedVideos(ctx, videos, accountID)
		if err != nil {
			return ListByFollowingResponse{}, err
		}
		resp := ListByFollowingResponse{
			VideoList: feedVideos,
			NextTime:  nextTime,
			HasMore:   hasMore,
		}
		return resp, nil
	}

	var cacheKey string
	// 如果redis缓存运行且用户登录
	if accountID != 0 && s.redisCache != nil {
		before := int64(0)
		if !latestBefore.IsZero() {
			before = latestBefore.Unix()
		}
		cacheKey = s.redisCache.Key("feed:listByFollowing:limit=%d:accountID=%d:before=%d", limit, accountID, before)
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		// 查询缓存
		bytes, err := s.redisCache.GetBytes(cacheCtx, cacheKey)
		if err == nil {
			var cached ListByFollowingResponse
			if err := json.Unmarshal(bytes, &cached); err == nil {
				// 缓存存在 直接返回
				return cached, nil
			}
		} else if rediscache.IsMiss(err) {
			// 缓存未命中
			// 获取分布式锁
			lockKey := "lock:" + cacheKey
			token, locked, _ := s.redisCache.Lock(cacheCtx, lockKey, 500*time.Millisecond)
			if locked {
				defer func() {
					s.redisCache.Unlock(context.Background(), lockKey, token)
				}()
				// 先再次检查缓存 缓存未命中和抢到锁的时间窗口内 可能缓存已经被其他协程写入
				if b, err := s.redisCache.GetBytes(cacheCtx, cacheKey); err == nil {
					var cached ListByFollowingResponse
					if err := json.Unmarshal(b, &cached); err == nil {
						return cached, nil
					}
				} else {
					// 查询数据库
					resp, err := doListByFollowingFromDB()
					if err != nil {
						return ListByFollowingResponse{}, err
					}
					// 回写缓存
					if b, err := json.Marshal(resp); err == nil {
						s.redisCache.SetBytes(cacheCtx, cacheKey, b, s.cacheTTL)
					}
					// 返回结果
					return resp, nil
				}
			} else {
				// 如果没竞争到锁 等待持有锁线程回写缓存
				for i := 0; i < 5; i++ {
					// 只试五次 超时降级查询mysql
					time.Sleep(20 * time.Millisecond)
					if b, err := s.redisCache.GetBytes(cacheCtx, cacheKey); err == nil {
						var cached ListByFollowingResponse
						if err := json.Unmarshal(b, &cached); err == nil {
							return cached, nil
						}
					}
				}
			}
		}
	}

	// 如果redis故障或者查询redis超时/出错 降级查询mysql
	resp, err := doListByFollowingFromDB()
	if err != nil {
		return ListByFollowingResponse{}, err
	}
	if cacheKey != "" {
		// 回写redis缓存
		if b, err := json.Marshal(resp); err == nil {
			cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			s.redisCache.SetBytes(cacheCtx, cacheKey, b, s.cacheTTL)
		}
	}
	return resp, nil
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
