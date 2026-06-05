//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/api/comment"
	"github.com/kiritosuki/GoVideo/internal/api/feed"
	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/message"
	"github.com/kiritosuki/GoVideo/internal/api/profile"
	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
)

type repoSet struct {
	account *account.AccountRepo
	video   *video.VideoRepo
	feed    *feed.FeedRepo
	like    *like.LikeRepo
	comment *comment.CommentRepo
	social  *social.SocialRepo
	message *message.MessageRepo
}

type mqSet struct {
	like       *rabbitmq.LikeMQ
	comment    *rabbitmq.CommentMQ
	social     *rabbitmq.SocialMQ
	popularity *rabbitmq.PopularityMQ
	timeline   *rabbitmq.TimelineMQ
}

func (e *integrationEnv) repos() repoSet {
	return repoSet{
		account: account.NewAccountRepo(e.db),
		video:   video.NewVideoRepo(e.db),
		feed:    feed.NewFeedRepo(e.db),
		like:    like.NewLikeRepo(e.db),
		comment: comment.NewCommentRepo(e.db),
		social:  social.NewSocialRepo(e.db),
		message: message.NewMessageRepo(e.db),
	}
}

func (e *integrationEnv) mqs(t *testing.T) mqSet {
	t.Helper()
	likeMQ, err := rabbitmq.NewLikeMQ(e.rmq)
	if err != nil {
		t.Fatalf("创建LikeMQ失败: %v", err)
	}
	commentMQ, err := rabbitmq.NewCommentMQ(e.rmq)
	if err != nil {
		t.Fatalf("创建CommentMQ失败: %v", err)
	}
	socialMQ, err := rabbitmq.NewSocialMQ(e.rmq)
	if err != nil {
		t.Fatalf("创建SocialMQ失败: %v", err)
	}
	popularityMQ, err := rabbitmq.NewPopularityMQ(e.rmq)
	if err != nil {
		t.Fatalf("创建PopularityMQ失败: %v", err)
	}
	timelineMQ, err := rabbitmq.NewTimelineMQ(e.rmq)
	if err != nil {
		t.Fatalf("创建TimelineMQ失败: %v", err)
	}
	return mqSet{
		like:       likeMQ,
		comment:    commentMQ,
		social:     socialMQ,
		popularity: popularityMQ,
		timeline:   timelineMQ,
	}
}

func (e *integrationEnv) accountService() *account.AccountService {
	rs := e.repos()
	return account.NewAccountService(rs.account, e.cache)
}

func (e *integrationEnv) feedService() *feed.FeedService {
	rs := e.repos()
	return feed.NewFeedService(rs.feed, rs.like, e.cache)
}

func (e *integrationEnv) videoService() *video.VideoService {
	rs := e.repos()
	return video.NewVideoService(rs.video, e.cache)
}

func (e *integrationEnv) profileService() *profile.ProfileService {
	rs := e.repos()
	return profile.NewProfileService(rs.account, rs.video, rs.social, e.cache)
}

func (e *integrationEnv) messageService() *message.MessageService {
	rs := e.repos()
	return message.NewMessageService(rs.message)
}

func (e *integrationEnv) createAccount(t *testing.T, username string) *account.Account {
	t.Helper()
	acc := &account.Account{
		Username: username,
		Password: "plain-password",
	}
	if err := e.db.WithContext(context.Background()).Create(acc).Error; err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	return acc
}

func (e *integrationEnv) createVideo(t *testing.T, author *account.Account, title string, createdAt time.Time) *video.Video {
	t.Helper()
	v := &video.Video{
		AuthorID:    author.ID,
		Username:    author.Username,
		Title:       title,
		Description: "integration video",
		PlayURL:     fmt.Sprintf("https://example.com/%s.mp4", title),
		CoverURL:    fmt.Sprintf("https://example.com/%s.jpg", title),
		CreateTime:  createdAt,
	}
	if err := e.db.WithContext(context.Background()).Create(v).Error; err != nil {
		t.Fatalf("创建测试视频失败: %v", err)
	}
	return v
}

func (e *integrationEnv) createComment(t *testing.T, author *account.Account, v *video.Video, eventID string) *comment.Comment {
	t.Helper()
	c := &comment.Comment{
		EventID:  eventID,
		Username: author.Username,
		VideoID:  v.ID,
		AuthorID: author.ID,
		Content:  "integration comment",
	}
	if err := e.db.WithContext(context.Background()).Create(c).Error; err != nil {
		t.Fatalf("创建测试评论失败: %v", err)
	}
	return c
}
