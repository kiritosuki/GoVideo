package worker

import (
	"context"
	"errors"
	"log"

	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	RetryHeader = "x-retry-count"
)

func consumeWithRetry(ctx context.Context, ch *amqp.Channel, queue string, workerName string, process func(context.Context, amqp.Delivery) error) error {
	// 获取消息队列的channel 消费消息需手动ack
	deliveries, err := ch.Consume(
		queue,
		"",
		false, // autoAck
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}
	for {
		// 循环消费消息
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("deliveries channel closed")
			}
			// 消费消息
			handleMainDelivery(ctx, ch, workerName, process, d)
		}
	}
}

// handleMainDelivery 消费普通消息 消费时调用process
func handleMainDelivery(ctx context.Context, ch *amqp.Channel, workerName string, process func(context.Context, amqp.Delivery) error, d amqp.Delivery) {
	if err := process(ctx, d); err != nil {
		// 消息消费失败 获取重试次数
		retryCount := getRetryCount(d)
		// 若超过重试上限 放入死信队列
		if retryCount >= rabbitmq.MaxRetryCount {
			log.Printf("%s worker: max retries exceeded (%d), moving to DLX: %v\n", workerName, retryCount, err)
			_ = d.Nack(false, false) // 第二个参数 true表示重新放回原队列 false表示放入死信队列(若没有死信队列则丢弃消息)
			return
		}
		// 若还有重试次数 计数器+1
		nextRetry := retryCount + 1
		log.Printf("%s worker: failed (retry %d/%d): %v\n", workerName, nextRetry, rabbitmq.MaxRetryCount, err)
		// 重新放回队列
		if err := republishForRetry(ctx, ch, d, nextRetry); err != nil {
			// 如果手动放回队列失败 利用Nack放回原队列(但这里计数器不会+1)
			_ = d.Nack(false, true)
			return
		}
		// 重试消息已经放回队列 原消息可以ack
		_ = d.Ack(false)
		return
	}
	// 消费普通消息成功 确认ack
	_ = d.Ack(false)
}

// getRetryCount 获取消息头中的重试次数
func getRetryCount(d amqp.Delivery) int {
	v, ok := d.Headers[RetryHeader]
	if !ok {
		return 0
	}
	cnts, ok := v.(int32)
	if !ok {
		return 0
	}
	return int(cnts)
}

// republishForRetry 将新的死信消息重新加入队列
func republishForRetry(ctx context.Context, ch *amqp.Channel, d amqp.Delivery, retryCount int) error {
	headers := amqp.Table{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	headers[RetryHeader] = int32(retryCount)

	return ch.PublishWithContext(ctx, d.Exchange, d.RoutingKey, false, false, amqp.Publishing{
		Headers:         headers,
		ContentType:     d.ContentType,
		ContentEncoding: d.ContentEncoding,
		DeliveryMode:    d.DeliveryMode,
		Priority:        d.Priority,
		CorrelationId:   d.CorrelationId,
		ReplyTo:         d.ReplyTo,
		Expiration:      d.Expiration,
		MessageId:       d.MessageId,
		Timestamp:       d.Timestamp,
		Type:            d.Type,
		UserId:          d.UserId,
		AppId:           d.AppId,
		Body:            d.Body,
	})
}
