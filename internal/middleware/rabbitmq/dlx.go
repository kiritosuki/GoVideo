package rabbitmq

import (
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	DLXExchange = "dlx.events"
)

// DeclareDLX 声明死信交换机和对应的死信队列
func DeclareDLX(ch *amqp.Channel, queueName string, bindingKey string) error {
	if ch == nil {
		return nil
	}
	// 声明死信exchange
	if err := ch.ExchangeDeclare(
		DLXExchange,
		"topic",
		true, // exchange持久化 rabbitmq服务重启后exchange仍然存在
		false,
		false,
		false,
		nil,
	); err != nil {
		return err
	}
	// 声明死信queue
	dlxQueue := queueName + ".dlx"
	q, err := ch.QueueDeclare(
		dlxQueue,
		true, // queue持久化 rabbitmq服务重启后queue仍然存在
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}
	// 绑定queue和exchange
	if err := ch.QueueBind(
		q.Name,
		bindingKey,
		DLXExchange,
		false,
		nil,
	); err != nil {
		return err
	}
	log.Printf("DLX ready: exchange=%s, queue=%s", DLXExchange, dlxQueue)
	return nil
}

// GetRetryCount 从 amqp x-death header 中提取当前消息已被重试的次数
//func GetRetryCount(d amqp.Delivery) int {
//	deaths, ok := d.Headers["x-death"].([]any)
//	if !ok || len(deaths) == 0 {
//		return 0
//	}
//	death, ok := deaths[0].(amqp.Table)
//	if !ok {
//		return 0
//	}
//	count, ok := death["count"].(int64)
//	if !ok {
//		return 0
//	}
//	return int(count)
//}
