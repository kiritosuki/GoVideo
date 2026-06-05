//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRabbitMQDeclareAndRouteEvents(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 声明全部业务MQ，验证交换机、队列、绑定关系在真实RabbitMQ中可重复声明。
	mqs := env.mqs(t)
	if _, err := rabbitmq.NewNotificationMQ(env.rmq); err != nil {
		t.Fatalf("声明NotificationMQ失败: %v", err)
	}

	if err := mqs.like.Like(ctx, 101, 201); err != nil {
		t.Fatalf("发布点赞事件失败: %v", err)
	}
	likeDelivery := consumeOne(t, env.newChannel(t), rabbitmq.LikeQueue, 3*time.Second)
	var likeEvt rabbitmq.LikeEvent
	if err := json.Unmarshal(likeDelivery.Body, &likeEvt); err != nil {
		t.Fatalf("点赞事件JSON反序列化失败: %v", err)
	}
	if likeEvt.Action != "like" || likeEvt.UserID != 101 || likeEvt.VideoID != 201 || likeEvt.EventID == "" {
		t.Fatalf("点赞事件内容异常: %+v", likeEvt)
	}

	// notification.like 队列绑定到 like.like，因此同一个点赞事件也应该路由到通知队列。
	notificationDelivery := consumeOne(t, env.newChannel(t), rabbitmq.NotificationLikeQueue, 3*time.Second)
	var notifLikeEvt rabbitmq.LikeEvent
	if err := json.Unmarshal(notificationDelivery.Body, &notifLikeEvt); err != nil {
		t.Fatalf("通知点赞事件JSON反序列化失败: %v", err)
	}
	if notifLikeEvt.EventID != likeEvt.EventID {
		t.Fatalf("通知队列收到的点赞事件应与原事件一致, got=%+v want=%+v", notifLikeEvt, likeEvt)
	}

	if err := mqs.comment.Publish(ctx, "commenter", 202, 102, "hello"); err != nil {
		t.Fatalf("发布评论事件失败: %v", err)
	}
	commentDelivery := consumeOne(t, env.newChannel(t), rabbitmq.CommentQueue, 3*time.Second)
	var commentEvt rabbitmq.CommentEvent
	if err := json.Unmarshal(commentDelivery.Body, &commentEvt); err != nil {
		t.Fatalf("评论事件JSON反序列化失败: %v", err)
	}
	if commentEvt.Action != "publish" || commentEvt.VideoID != 202 || commentEvt.AuthorID != 102 || commentEvt.EventID == "" {
		t.Fatalf("评论事件内容异常: %+v", commentEvt)
	}
	_ = consumeOne(t, env.newChannel(t), rabbitmq.NotificationCommentQueue, 3*time.Second)

	if err := mqs.social.Follow(ctx, 103, 104); err != nil {
		t.Fatalf("发布关注事件失败: %v", err)
	}
	socialDelivery := consumeOne(t, env.newChannel(t), rabbitmq.SocialQueue, 3*time.Second)
	var socialEvt rabbitmq.SocialEvent
	if err := json.Unmarshal(socialDelivery.Body, &socialEvt); err != nil {
		t.Fatalf("关注事件JSON反序列化失败: %v", err)
	}
	if socialEvt.Action != "follow" || socialEvt.FollowerID != 103 || socialEvt.VloggerID != 104 || socialEvt.EventID == "" {
		t.Fatalf("关注事件内容异常: %+v", socialEvt)
	}
	_ = consumeOne(t, env.newChannel(t), rabbitmq.NotificationSocialQueue, 3*time.Second)
}

func TestRabbitMQIndependentChannels(t *testing.T) {
	env := setupIntegration(t)

	clientA, err := env.rmq.NewChannelClient()
	if err != nil {
		t.Fatalf("创建channel A失败: %v", err)
	}
	clientB, err := env.rmq.NewChannelClient()
	if err != nil {
		t.Fatalf("创建channel B失败: %v", err)
	}
	defer clientB.CloseChannel()

	// 关闭一个独立channel后，另一个channel仍然应该可以声明队列。
	if err := clientA.CloseChannel(); err != nil {
		t.Fatalf("关闭channel A失败: %v", err)
	}
	if _, err := clientB.Ch.QueueDeclare("it.channel.alive", false, true, true, false, nil); err != nil {
		t.Fatalf("channel B不应受channel A关闭影响: %v", err)
	}
}

func consumeOne(t *testing.T, ch *amqp.Channel, queue string, timeout time.Duration) amqp.Delivery {
	t.Helper()
	deliveries, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		t.Fatalf("消费队列 %s 失败: %v", queue, err)
	}
	select {
	case d := <-deliveries:
		if err := d.Ack(false); err != nil {
			t.Fatalf("ack队列 %s 消息失败: %v", queue, err)
		}
		return d
	case <-time.After(timeout):
		t.Fatalf("超时未从队列 %s 收到消息", queue)
		return amqp.Delivery{}
	}
}
