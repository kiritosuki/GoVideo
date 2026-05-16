package comment

import (
	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/apierror"
	"github.com/kiritosuki/GoVideo/internal/middleware/jwt"
)

type CommentHandler struct {
	commentService *CommentService
	accountService *account.AccountService
}

func NewCommentHandler(commentService *CommentService, accountService *account.AccountService) *CommentHandler {
	return &CommentHandler{
		commentService: commentService,
		accountService: accountService,
	}
}

// PublishComment 发布评论
func (h *CommentHandler) PublishComment(c *gin.Context) {
	var req PublishCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if req.Content == "" {
		c.JSON(400, gin.H{"error": "content is required"})
		return
	}
	if req.VideoID <= 0 {
		c.JSON(400, gin.H{"error": "video_id is required"})
		return
	}
	// 获取当前用户id
	authorID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	username, err := jwt.GetUsername(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	comment := &Comment{
		Username: username,
		VideoID:  req.VideoID,
		AuthorID: authorID,
		Content:  req.Content,
	}
	if err := h.commentService.Publish(c.Request.Context(), comment); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "comment published successfully"})
}

// DeleteComment 删除评论
func (h *CommentHandler) DeleteComment(c *gin.Context) {
	var req DeleteCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	// 获取当前用户id
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if req.CommentID <= 0 {
		c.JSON(400, gin.H{"error": "comment_id is required"})
		return
	}
	if err := h.commentService.Delete(c.Request.Context(), req.CommentID, accountID); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "comment deleted successfully"})
}

// ListAllComments 列出某条视频的全部评论
func (h *CommentHandler) ListAllComments(c *gin.Context) {
	var req GetAllCommentsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if req.VideoID == 0 {
		c.JSON(400, gin.H{"error": "video_id is required"})
		return
	}
	// 获取该视频的所有评论
	comments, err := h.commentService.ListAll(c.Request.Context(), req.VideoID)
	if err != nil {
		c.JSON(apierror.ClassifyHttpStatus(err), gin.H{"error": err.Error()})
		return
	}
	if comments == nil {
		comments = []Comment{}
	}
	c.JSON(200, comments)
}
