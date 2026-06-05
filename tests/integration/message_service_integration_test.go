//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/kiritosuki/GoVideo/internal/api/message"
)

func TestMessageServiceSendAndList(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	alice := env.createAccount(t, "message-alice")
	bob := env.createAccount(t, "message-bob")
	svc := env.messageService()

	if err := svc.Send(ctx, &message.Message{FromID: alice.ID, ToID: bob.ID, Content: "   "}); err == nil {
		t.Fatalf("空消息内容应该被拒绝")
	}
	if err := svc.Send(ctx, &message.Message{FromID: alice.ID, ToID: bob.ID, Content: " hello bob "}); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}
	if err := svc.Send(ctx, &message.Message{FromID: bob.ID, ToID: alice.ID, Content: "hello alice"}); err != nil {
		t.Fatalf("发送回复消息失败: %v", err)
	}

	msgs, err := svc.List(ctx, alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("查询会话消息失败: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("会话应该返回两条消息, got=%+v", msgs)
	}
	if msgs[0].Content == "" || msgs[1].Content == "" {
		t.Fatalf("消息内容不应该为空: %+v", msgs)
	}
}
