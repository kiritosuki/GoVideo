package video

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/kiritosuki/GoVideo/internal/api/tag"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"gorm.io/gorm"
)

type VideoService struct {
	videoRepo *VideoRepo
	cache     *rediscache.Client
}

func NewVideoService(videoRepo *VideoRepo, cache *rediscache.Client) *VideoService {
	return &VideoService{
		videoRepo: videoRepo,
		cache:     cache,
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
			Status:     "pending",
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
