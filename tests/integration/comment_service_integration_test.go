//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/comment"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
)

func TestCommentServicePublishesMQWhenAvailable(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "comment-service-author")
	user := env.createAccount(t, "comment-service-user")
	v := env.createVideo(t, author, "comment-service-video", time.Now())
	rs := env.repos()
	mqs := env.mqs(t)
	svc := comment.NewCommentService(rs.comment, rs.video, env.cache, mqs.comment, mqs.popularity)

	if err := svc.Publish(ctx, &comment.Comment{Username: user.Username, VideoID: v.ID, AuthorID: user.ID, Content: " hello "}); err != nil {
		t.Fatalf("评论投递MQ失败: %v", err)
	}

	// MQ路径下service不直接写评论表，等待CommentWorker异步消费。
	comments, err := rs.comment.ListAllComments(ctx, v.ID)
	if err != nil {
		t.Fatalf("查询评论失败: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("MQ路径下service不应直接写comments表, got=%+v", comments)
	}

	d := consumeOne(t, env.newChannel(t), rabbitmq.CommentQueue, 3*time.Second)
	var evt rabbitmq.CommentEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		t.Fatalf("评论MQ消息反序列化失败: %v", err)
	}
	if evt.Action != "publish" || evt.VideoID != v.ID || evt.AuthorID != user.ID || strings.TrimSpace(evt.Content) != "hello" {
		t.Fatalf("评论MQ消息内容异常: %+v", evt)
	}
	_ = consumeOne(t, env.newChannel(t), rabbitmq.PopularityQueue, 3*time.Second)
}

func TestCommentServiceFallbackWritesDBWithMockEventID(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "comment-fallback-author")
	user := env.createAccount(t, "comment-fallback-user")
	v := env.createVideo(t, author, "comment-fallback-video", time.Now())
	rs := env.repos()
	svc := comment.NewCommentService(rs.comment, rs.video, env.cache, nil, nil)

	if err := svc.Publish(ctx, &comment.Comment{Username: user.Username, VideoID: v.ID, AuthorID: user.ID, Content: "fallback comment"}); err != nil {
		t.Fatalf("MQ不可用时评论fallback失败: %v", err)
	}
	comments, err := rs.comment.ListAllComments(ctx, v.ID)
	if err != nil {
		t.Fatalf("查询评论失败: %v", err)
	}
	if len(comments) != 1 || !strings.HasPrefix(comments[0].EventID, "mock:") {
		t.Fatalf("fallback评论应写入mock:event_id, got=%+v", comments)
	}
}

func TestCommentServiceDeleteAuthAndFallback(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	author := env.createAccount(t, "comment-delete-author")
	other := env.createAccount(t, "comment-delete-other")
	v := env.createVideo(t, author, "comment-delete-video", time.Now())
	c := env.createComment(t, author, v, "comment-delete-event")
	rs := env.repos()
	svc := comment.NewCommentService(rs.comment, rs.video, env.cache, nil, nil)

	if err := svc.Delete(ctx, c.ID, other.ID); err == nil {
		t.Fatalf("非评论作者不应该能删除评论")
	}
	if err := svc.Delete(ctx, c.ID, author.ID); err != nil {
		t.Fatalf("MQ不可用时删除评论fallback失败: %v", err)
	}
	got, err := rs.comment.FindByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("查询评论失败: %v", err)
	}
	if got != nil {
		t.Fatalf("评论应该已被删除, got=%+v", got)
	}
}
