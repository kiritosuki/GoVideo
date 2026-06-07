package video

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/tag"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

type VideoService struct {
	videoRepo    *VideoRepo
	cache        *rediscache.Client
	cacheTTL     time.Duration
	requestGroup singleflight.Group
}

func NewVideoService(videoRepo *VideoRepo, cache *rediscache.Client) *VideoService {
	return &VideoService{
		videoRepo: videoRepo,
		cache:     cache,
		cacheTTL:  5 * time.Minute,
	}
}

// Publish 发布视频
// 将video插入数据库 创建outbox消息插入数据库 将video_tag插入数据库
// 用事务保证了一致性
func (s *VideoService) Publish(ctx context.Context, video *Video) error {
	if video == nil {
		return errors.New("video is nil")
	}
	video.Title = strings.TrimSpace(video.Title)
	video.PlayURL = strings.TrimSpace(video.PlayURL)
	video.CoverURL = strings.TrimSpace(video.CoverURL)
	if video.Title == "" {
		return errors.New("title is required")
	}
	if video.PlayURL == "" {
		return errors.New("play_url is required")
	}
	if video.CoverURL == "" {
		return errors.New("cover url is required")
	}
	// 将视频写入库 将消息写入本地表
	// 用事务保证一致性
	err := s.videoRepo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 将视频写入数据库
		if err := tx.Create(video).Error; err != nil {
			return err
		}
		// 创建outbox代办消息
		msg := &OutboxMsg{
			VideoID:    video.ID,
			EventType:  "video_published",
			CreateTime: video.CreateTime,
			Status:     OutboxStatusPending,
		}
		// 将outbox消息写入数据库
		if err := tx.Create(msg).Error; err != nil {
			return err
		}
		// 提取视频标题和简介中的标签
		tags := extractTags(video.Title + " " + video.Description)
		for _, tagName := range tags {
			// 如果数据库中存在该标签则获取 不存在则插入
			var t tag.Tag
			err := tx.Where("name = ?", tagName).FirstOrCreate(&t, &tag.Tag{
				Name: tagName,
			}).Error
			if err != nil {
				return err
			}
			// 将video_tag插入数据库
			err = tx.Create(&tag.VideoTag{
				VideoID: video.ID,
				TagID:   t.ID,
			}).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// ListByAuthorID 根据作者ID查询视频列表
func (s *VideoService) ListByAuthorID(ctx context.Context, authorID uint) ([]Video, error) {
	return s.videoRepo.ListByAuthorID(ctx, authorID)
}

// GetDetail 根据id获取视频详细信息
func (s *VideoService) GetDetail(ctx context.Context, id uint) (*Video, error) {
	// 若redis缓存启用了
	if s.cache != nil {
		cacheKey := s.cache.Key("video:detail:id=%d", id)
		// 从redis缓存中查询视频的函数
		getCacheFunc := func() (*Video, bool) {
			cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			bytes, err := s.cache.GetBytes(cacheCtx, cacheKey)
			cancel()
			if err != nil {
				return nil, false
			}
			var video Video
			if err := json.Unmarshal(bytes, &video); err != nil {
				return nil, false
			}
			return &video, true
		}
		// 添加redis缓存视频的函数
		setCacheFunc := func(video *Video) {
			bytes, err := json.Marshal(video)
			if err != nil {
				return
			}
			cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			s.cache.SetBytes(cacheCtx, cacheKey, bytes, s.cacheTTL)
			cancel()
		}
		// 先尝试从redis缓存中查询视频
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		bytes, err := s.cache.GetBytes(cacheCtx, cacheKey)
		cancel()
		if err == nil {
			// 若缓存存在
			var cached Video
			if err := json.Unmarshal(bytes, &cached); err == nil {
				return &cached, nil
			}
		} else if rediscache.IsMiss(err) {
			// 若缓存未命中 先用singleflight合并本进程内的重复请求
			// 合并后的请求再竞争redis分布式锁 保护多节点下的缓存重建
			sfKey := s.cache.Key("sf:video:detail:id=%d", id)
			v, err, _ := s.requestGroup.Do(sfKey, func() (interface{}, error) {
				// 进入singleflight后再次查询缓存 避免等待期间已有请求回写缓存
				if cached, ok := getCacheFunc(); ok {
					return cached, nil
				}

				lockKey := s.cache.Key("lock:video:detail:id=%d", id)
				lockCtx, lockCancel := context.WithTimeout(ctx, 50*time.Millisecond)
				token, locked, lockErr := s.cache.Lock(lockCtx, lockKey, 2*time.Second)
				lockCancel()
				if lockErr != nil {
					// redis锁异常时不再等待缓存回填 直接降级查询数据库
					video, err := s.videoRepo.FindByID(ctx, id)
					if err != nil {
						return nil, err
					}
					setCacheFunc(video)
					return video, nil
				}
				if lockErr == nil && locked {
					// 如果抢到了锁 查询数据库并回写缓存
					defer func() {
						s.cache.Unlock(context.Background(), lockKey, token)
					}()
					// 抢到锁后再次查询缓存 避免锁竞争期间其他节点已经回写缓存
					if cached, ok := getCacheFunc(); ok {
						return cached, nil
					}
					video, err := s.videoRepo.FindByID(ctx, id)
					if err != nil {
						return nil, err
					}
					setCacheFunc(video)
					return video, nil
				}

				// 如果没有抢到锁 反复查询缓存 等待其他请求/节点回写缓存
				for i := 0; i < 5; i++ {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(20 * time.Millisecond):
					}
					if video, ok := getCacheFunc(); ok {
						return video, nil
					}
				}

				// 若20 * 5 = 100ms内没查询到缓存 则降级查询数据库并尝试回写缓存
				video, err := s.videoRepo.FindByID(ctx, id)
				if err != nil {
					return nil, err
				}
				setCacheFunc(video)
				return video, nil
			})
			if err != nil {
				return nil, err
			}
			if video, ok := v.(*Video); ok {
				return video, nil
			}
			return nil, errors.New("unexpected video detail singleflight result")
		} // 其他err即为redis宕机 正常查询数据库即可
		// redis缓存内容异常时 直接降级查询数据库并回写缓存
		video, err := s.videoRepo.FindByID(ctx, id)
		if err != nil {
			return nil, err
		}
		setCacheFunc(video)
		return video, nil
	}
	// redis未启用 直接查询数据库
	return s.videoRepo.FindByID(ctx, id)
}

/* 辅助函数 */
// extractTags 提取文本中的标签 即#xxx样式 返回xxx的字符串切片
func extractTags(text string) []string {
	// 用于匹配标签的正则表达式
	// 匹配以#开头的字符串 字符串包括各种语言/数字/下划线
	tagRegex := regexp.MustCompile(`#([\p{L}\p{N}_]+)`)
	seen := make(map[string]bool)
	var tags []string
	// 进行文本的正则匹配 -1表示匹配所有项
	// 匹配后的结果如下：
	// [
	//	[#go, go],
	//	[#redis, redis],
	// ]
	matches := tagRegex.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		t := m[1]
		if !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	return tags
}
