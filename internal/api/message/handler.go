package message

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/apierror"
	"github.com/kiritosuki/GoVideo/internal/middleware/jwt"
)

type MessageHandler struct {
	messageService *MessageService
}

func NewMessageHandler(messageService *MessageService) *MessageHandler {
	return &MessageHandler{
		messageService: messageService,
	}
}

// Send 发送信息 JWT鉴权
func (h *MessageHandler) Send(c *gin.Context) {
	// 获取用户id
	fromID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req SendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ToID == 0 || strings.TrimSpace(req.Content) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to_id and content are required"})
		return
	}
	m := &Message{
		FromID:  fromID,
		ToID:    req.ToID,
		Content: req.Content,
	}
	if err := h.messageService.Send(c.Request.Context(), m); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, SendResponse{
		Message: m,
	})
}

// List 列出消息 JWT鉴权
func (h *MessageHandler) List(c *gin.Context) {
	// 获取用户id
	userID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req ListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.PeerID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer_id is required"})
		return
	}
	msgs, err := h.messageService.List(c.Request.Context(), userID, req.PeerID)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if msgs == nil {
		msgs = []Message{}
	}
	c.JSON(http.StatusOK, ListResponse{
		Messages: msgs,
	})
}
