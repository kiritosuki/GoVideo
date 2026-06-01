package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/kiritosuki/GoVideo/internal/config"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	MaxRetryCount = 3
)

type RabbitMQ struct {
	Conn *amqp.Connection
	Ch   *amqp.Channel
}

func NewRabbitMQ(cfg *config.RabbitMQConfig) (*RabbitMQ, error) {
	if cfg == nil {
		return nil, errors.New("rabbitmq config is nil")
	}
	// url = amqp://username:password@host:port
	url := "amqp://" + cfg.Username + ":" + cfg.Password + "@" + cfg.Host + ":" + strconv.Itoa(cfg.Port)
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	return &RabbitMQ{
		Conn: conn,
		Ch:   ch,
	}, nil
}

func (r *RabbitMQ) Close() error {
	if r == nil || r.Conn == nil || r.Ch == nil {
		return nil
	}
	if err := r.Ch.Close(); err != nil {
		return err
	}
	if err := r.Conn.Close(); err != nil {
		return err
	}
	return nil
}

// DeclareTopic 声明topic类型交换机和队列
func (r *RabbitMQ) DeclareTopic(exchange string, queue string, bindingKey string) error {
	if r == nil || r.Conn == nil || r.Ch == nil {
		return errors.New("rabbitmq is not initialized")
	}
	if exchange == "" || queue == "" || bindingKey == "" {
		return errors.New("exchange/queue/bindingKey is required")
	}
	// 声明exchange
	if err := r.Ch.ExchangeDeclare(
		exchange,
		"topic",
		true, // exchange持久化 rabbitmq服务重启后exchange仍然存在
		false,
		false,
		false,
		nil,
	); err != nil {
		return err
	}
	// 声明queue
	q, err := r.Ch.QueueDeclare(
		queue,
		true, // queue持久化 rabbitmq服务重启后queue仍然存在
		false,
		false,
		false,
		amqp.Table{
			"x-dead-letter-exchange": DLXExchange,
		},
	)
	if err != nil {
		return err
	}
	// 绑定queue和exchange
	if err := r.Ch.QueueBind(
		q.Name,
		bindingKey,
		exchange,
		false,
		nil,
	); err != nil {
		return err
	}
	// 声明死信交换机和死信队列
	if err := DeclareDLX(r.Ch, queue, bindingKey); err != nil {
		log.Printf("DLX declare failed for %s: %v", queue, err)
	}
	return nil
}

// PublishJSON 向消息队列里发送信息
func (r *RabbitMQ) PublishJSON(ctx context.Context, exchange string, routingKey string, payload any) error {
	if r == nil || r.Ch == nil {
		return errors.New("rabbitmq is not initialized")
	}
	if exchange == "" || routingKey == "" {
		return errors.New("exchange and routingKey are required")
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return r.Ch.PublishWithContext(ctx, exchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         bytes,
	})
}
