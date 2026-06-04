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
	"gorm.io/gorm"
)

type VideoService struct {
	videoRepo *VideoRepo
	cache     *rediscache.Client
	cacheTTL  time.Duration
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
	// 从redis缓存中查询视频的函数
	getCacheFunc := func() (*Video, bool) {
		key := s.cache.Key("video:detail:id=%d", id)
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		bytes, err := s.cache.GetBytes(cacheCtx, key)
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
		key := s.cache.Key("video:detail:id=%d", id)
		bytes, err := json.Marshal(video)
		if err != nil {
			return
		}
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		s.cache.SetBytes(cacheCtx, key, bytes, s.cacheTTL)
		cancel()
	}
	// 若redis缓存启用了
	if s.cache != nil {
		// 先尝试从redis缓存中查询视频
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		bytes, err := s.cache.GetBytes(cacheCtx, s.cache.Key("video:detail:id=%d", id))
		cancel()
		if err == nil {
			// 若缓存存在
			var cached Video
			if err := json.Unmarshal(bytes, &cached); err == nil {
				return &cached, nil
			}
		} else if rediscache.IsMiss(err) {
			// 若缓存未命中 多协程竞争分布式锁
			lockKey := s.cache.Key("lock:video:detail:id=%d", id)
			lockCtx, lockCancel := context.WithTimeout(ctx, 50*time.Millisecond)
			token, ok, err := s.cache.Lock(lockCtx, lockKey, 2*time.Second)
			lockCancel()
			if err == nil && ok {
				// 如果抢到了锁 查询数据库并回写缓存
				defer func() {
					s.cache.Unlock(context.Background(), lockKey, token)
				}()
				// 先再次查询缓存 缓存未命中与抢到锁的时间窗口内 可能缓存已经被其他协程写入
				if cached, ok := getCacheFunc(); ok {
					return cached, nil
				}
				// 查询数据库
				video, err := s.videoRepo.FindByID(ctx, id)
				if err != nil {
					return nil, err
				}
				// 回写缓存
				setCacheFunc(video)
				// 返回
				return video, nil
			} else {
				// 如果没有抢到锁 反复查询缓存 等待其他协程写入缓存
				for i := 0; i < 5; i++ {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(20 * time.Millisecond):
					}
					// 每次隔20ms尝试查询一次缓存
					if video, ok := getCacheFunc(); ok {
						return video, nil
					}
				}
				// 若20 * 5 = 100ms内没查询到缓存 则降级去数据库查询
			}
		} // 其他err即为redis宕机 正常查询数据库即可
	}
	// 查询数据库
	video, err := s.videoRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 如果redis缓存启用了 回写缓存
	if s.cache != nil {
		setCacheFunc(video)
	}
	// 返回
	return video, nil
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
