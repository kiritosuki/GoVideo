//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/comment"
	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/notification"
	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/util"
)

func TestDBSchemaUniqueIndexes(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()

	author := env.createAccount(t, "schema-author")
	viewer := env.createAccount(t, "schema-viewer")
	v := env.createVideo(t, author, "schema-video", time.Now())

	// likes 表依赖 video_id + account_id 唯一索引保证重复点赞不会重复落库。
	firstLike := &like.Like{VideoID: v.ID, AccountID: viewer.ID, CreatedAt: time.Now()}
	secondLike := &like.Like{VideoID: v.ID, AccountID: viewer.ID, CreatedAt: time.Now()}
	if err := env.db.WithContext(ctx).Create(firstLike).Error; err != nil {
		t.Fatalf("首次点赞插入失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Create(secondLike).Error; !util.IsDupKey(err) {
		t.Fatalf("重复点赞应触发唯一索引错误, got: %v", err)
	}

	// socials 表依赖 follower_id + vlogger_id 唯一索引保证重复关注不会重复落库。
	firstSocial := &social.Social{FollowerID: viewer.ID, VloggerID: author.ID}
	secondSocial := &social.Social{FollowerID: viewer.ID, VloggerID: author.ID}
	if err := env.db.WithContext(ctx).Create(firstSocial).Error; err != nil {
		t.Fatalf("首次关注插入失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Create(secondSocial).Error; !util.IsDupKey(err) {
		t.Fatalf("重复关注应触发唯一索引错误, got: %v", err)
	}

	// comments.event_id 是评论消费幂等的核心约束。
	firstComment := &comment.Comment{EventID: "schema-comment-event", Username: viewer.Username, VideoID: v.ID, AuthorID: viewer.ID, Content: "hello"}
	secondComment := &comment.Comment{EventID: "schema-comment-event", Username: viewer.Username, VideoID: v.ID, AuthorID: viewer.ID, Content: "hello again"}
	if err := env.db.WithContext(ctx).Create(firstComment).Error; err != nil {
		t.Fatalf("首次评论插入失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Create(secondComment).Error; !util.IsDupKey(err) {
		t.Fatalf("重复event_id评论应触发唯一索引错误, got: %v", err)
	}

	// notifications.event_id 是通知worker消费幂等的核心约束。
	firstNotif := &notification.Notification{EventID: "schema-notification-event", RecipientID: author.ID, SenderID: viewer.ID, Type: "like", TargetID: v.ID}
	secondNotif := &notification.Notification{EventID: "schema-notification-event", RecipientID: author.ID, SenderID: viewer.ID, Type: "like", TargetID: v.ID}
	if err := env.db.WithContext(ctx).Create(firstNotif).Error; err != nil {
		t.Fatalf("首次通知插入失败: %v", err)
	}
	if err := env.db.WithContext(ctx).Create(secondNotif).Error; !util.IsDupKey(err) {
		t.Fatalf("重复event_id通知应触发唯一索引错误, got: %v", err)
	}
}

func TestDBSchemaOutboxFields(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()

	author := env.createAccount(t, "outbox-schema-author")
	v := env.createVideo(t, author, "outbox-schema-video", time.Now())
	now := time.Now()
	msg := &video.OutboxMsg{
		VideoID:     v.ID,
		EventType:   "video_published",
		CreateTime:  now,
		Status:      video.OutboxStatusProcessing,
		RetryCount:  2,
		LastError:   "temporary rabbitmq error",
		UpdatedAt:   now,
		PublishedAt: &now,
	}
	if err := env.db.WithContext(ctx).Create(msg).Error; err != nil {
		t.Fatalf("写入outbox状态字段失败: %v", err)
	}

	var got video.OutboxMsg
	if err := env.db.WithContext(ctx).First(&got, msg.ID).Error; err != nil {
		t.Fatalf("读取outbox消息失败: %v", err)
	}
	if got.Status != video.OutboxStatusProcessing || got.RetryCount != 2 || got.LastError == "" || got.PublishedAt == nil {
		t.Fatalf("outbox状态字段读写异常: %+v", got)
	}
}
