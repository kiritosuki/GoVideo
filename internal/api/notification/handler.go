package notification

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/auth"
)

func (h *SSEHub) Push(userID uint, n *Notification) {
	h.mu.RLock()
	chs, ok := h.clients[userID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	for _, ch := range chs {
		select {
		case ch <- n:
		default:
		}
	}
}

// Subscribe 订阅一个新连接 用于消息推送
func (h *SSEHub) Subscribe(userID uint) chan *Notification {
	ch := make(chan *Notification, 20)
	h.mu.Lock()
	h.clients[userID] = append(h.clients[userID], ch)
	h.mu.Unlock()
	return ch
}

// Unsubscribe 关闭一个连接
func (h *SSEHub) Unsubscribe(userID uint, ch chan *Notification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	chs := h.clients[userID]
	for i, c := range chs {
		if c == ch {
			h.clients[userID] = append(chs[:i], chs[i+1:]...)
			close(c)
			return
		}
	}
}

// SSERequireAuth 中间件 做JWT鉴权
// SSE请求的header中可能不方便携带token 因此多做了在query参数中也会查询token
func (h *SSEHub) SSERequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Query("token")
		if token == "" {
			token = c.GetHeader("Authorization")
			if len(token) > 7 && token[:7] == "Bearer " {
				token = token[7:]
			}
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := auth.ParseToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set("accountID", claims.AccountID)
		c.Next()
	}
}

// SSEHandler 与前端建立SSE长连接
// SSE本质还是请求响应模型 因此这里是Handler 处理前端的连接请求
func (h *SSEHub) SSEHandler(c *gin.Context) {
	accountID, _ := c.Get("accountID")
	userID := accountID.(uint)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	ch := h.Subscribe(userID)
	defer h.Unsubscribe(userID, ch)

	ctx := c.Request.Context()
	flusher, _ := c.Writer.(http.Flusher)

	// 循环阻塞从通道获取通知
	// 如果间隔30s没有新通知会发送一个keepalive作为长连接信号
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(n)
			fmt.Fprintf(c.Writer, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
		case <-time.After(30 * time.Second):
			fmt.Fprintf(c.Writer, ": keepalive\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// ListHandler 列出最新的50条通知
// 可以用于离线/历史通知查询
func (h *SSEHub) ListHandler(c *gin.Context) {
	accountID, _ := c.Get("accountID")
	userID := accountID.(uint)

	var notifications []Notification
	if err := h.db.WithContext(c.Request.Context()).
		Where("recipient_id = ?", userID).
		Order("created_at desc").
		Limit(50).
		Find(&notifications).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if notifications == nil {
		notifications = []Notification{}
	}
	c.JSON(200, gin.H{"notifications": notifications})
}

// MarkReadHandler 标记通知消息为已读
// 传入ID表示标记一条消息为已读
// 不传表示标记所有消息为已读
func (h *SSEHub) MarkReadHandler(c *gin.Context) {
	accountID, _ := c.Get("accountID")
	userID := accountID.(uint)

	var req struct {
		ID *uint `json:"id"`
	}
	c.ShouldBindJSON(&req)

	if req.ID != nil {
		h.db.WithContext(c.Request.Context()).
			Model(&Notification{}).
			Where("id = ? AND recipient_id = ?", *req.ID, userID).
			Update("is_read", true)
	} else {
		h.db.WithContext(c.Request.Context()).
			Model(&Notification{}).
			Where("recipient_id = ?", userID).
			Update("is_read", true)
	}
	c.JSON(200, gin.H{"message": "ok"})
}

// UnreadCountHandler 获取未读的消息数
func (h *SSEHub) UnreadCountHandler(c *gin.Context) {
	accountID, _ := c.Get("accountID")
	userID := accountID.(uint)

	var count int64
	h.db.WithContext(c.Request.Context()).
		Model(&Notification{}).
		Where("recipient_id = ? AND is_read = ?", userID, false).
		Count(&count)
	c.JSON(200, gin.H{"count": count})
}
