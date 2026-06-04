package notification

import (
	"sync"
	"time"

	"gorm.io/gorm"
)

const PushChannel = "notification:push"

type Notification struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	EventID     string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"event_id"`
	RecipientID uint      `gorm:"index;not null" json:"recipient_id"`
	SenderID    uint      `gorm:"not null" json:"sender_id"`
	Type        string    `gorm:"type:varchar(50);not null" json:"type"`
	TargetID    uint      `json:"target_id"`
	Content     string    `gorm:"type:varchar(255)" json:"content"`
	IsRead      bool      `gorm:"default:false" json:"is_read"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

type NotificationHub interface {
	Push(userID uint, n *Notification)
}

type SSEHub struct {
	mu      sync.RWMutex
	clients map[uint][]chan *Notification
	db      *gorm.DB
}

func NewSSEHub(db *gorm.DB) *SSEHub {
	return &SSEHub{
		clients: make(map[uint][]chan *Notification),
		db:      db,
	}
}

// 这行代码声明一个不用的变量 关键是把*SSEHub赋值给了接口对象
// 可以在编译时期检查*SSEHub有没有实现接口
var _ NotificationHub = (*SSEHub)(nil)

type PushMessage struct {
	RecipientID  uint         `json:"recipient_id"`
	Notification Notification `json:"notification"`
}
