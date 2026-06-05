//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/api/comment"
	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/notification"
	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	apphttp "github.com/kiritosuki/GoVideo/internal/http"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

func TestAPISmokeMainUserFlow(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gin.SetMode(gin.TestMode)
	router, _ := apphttp.SetRouter(env.db, env.cache, env.rmq, nil)
	startAPISmokeWorkers(t, env, ctx)

	// 注册两个用户：作者负责发布视频，观众负责点赞、评论和关注。
	authorUsername := "api-author"
	viewerUsername := "api-viewer"
	apiPost(t, router, "/account/register", "", map[string]any{
		"username": authorUsername,
		"password": "password",
	}, http.StatusOK, nil)
	apiPost(t, router, "/account/register", "", map[string]any{
		"username": viewerUsername,
		"password": "password",
	}, http.StatusOK, nil)

	authorLogin := apiLogin(t, router, authorUsername, "password")
	viewerLogin := apiLogin(t, router, viewerUsername, "password")

	// 使用作者token发布视频。这里直接传入URL，不走文件上传/COS。
	var published video.Video
	apiPost(t, router, "/video/publish", authorLogin.Token, map[string]any{
		"title":       "api smoke #apitest",
		"description": "api smoke video",
		"play_url":    "https://example.com/api-smoke.mp4",
		"cover_url":   "https://example.com/api-smoke.jpg",
	}, http.StatusOK, &published)
	if published.ID == 0 || published.AuthorID != authorLogin.AccountID {
		t.Fatalf("发布视频响应异常: %+v", published)
	}

	// 拉取最新feed，验证HTTP层可以通过soft auth进入feed service。
	var latest struct {
		VideoList []struct {
			ID      uint `json:"id"`
			IsLiked bool `json:"is_liked"`
		} `json:"video_list"`
	}
	apiPost(t, router, "/feed/listLatest", viewerLogin.Token, map[string]any{
		"limit": 10,
	}, http.StatusOK, &latest)
	if len(latest.VideoList) == 0 || latest.VideoList[0].ID != published.ID {
		t.Fatalf("feed listLatest未返回刚发布的视频: %+v", latest)
	}

	// 观众点赞视频。接口本身只投递MQ，worker异步消费后才会落库和更新计数。
	apiPost(t, router, "/like/like", viewerLogin.Token, map[string]any{
		"video_id": published.ID,
	}, http.StatusOK, nil)
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&like.Like{}).Where("video_id = ? and account_id = ?", published.ID, viewerLogin.AccountID).Count(&count)
		return count == 1
	})
	var isLiked struct {
		IsLiked bool `json:"is_liked"`
	}
	apiPost(t, router, "/like/isLiked", viewerLogin.Token, map[string]any{
		"video_id": published.ID,
	}, http.StatusOK, &isLiked)
	if !isLiked.IsLiked {
		t.Fatalf("点赞后isLiked应返回true")
	}

	// 观众评论视频，等待CommentWorker异步写库。
	apiPost(t, router, "/comment/publish", viewerLogin.Token, map[string]any{
		"video_id": published.ID,
		"content":  "api smoke comment",
	}, http.StatusOK, nil)
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&comment.Comment{}).Where("video_id = ? and author_id = ?", published.ID, viewerLogin.AccountID).Count(&count)
		return count == 1
	})
	var comments []comment.Comment
	apiPost(t, router, "/comment/listAll", "", map[string]any{
		"video_id": published.ID,
	}, http.StatusOK, &comments)
	if len(comments) != 1 || comments[0].Content != "api smoke comment" {
		t.Fatalf("评论列表响应异常: %+v", comments)
	}

	// 观众关注作者，等待SocialWorker异步写库。
	apiPost(t, router, "/social/follow", viewerLogin.Token, map[string]any{
		"vlogger_id": authorLogin.AccountID,
	}, http.StatusOK, nil)
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&social.Social{}).Where("follower_id = ? and vlogger_id = ?", viewerLogin.AccountID, authorLogin.AccountID).Count(&count)
		return count == 1
	})
	var following struct {
		VideoList []struct {
			ID uint `json:"id"`
		} `json:"video_list"`
	}
	apiPost(t, router, "/feed/listByFollowing", viewerLogin.Token, map[string]any{
		"limit": 10,
	}, http.StatusOK, &following)
	if len(following.VideoList) == 0 || following.VideoList[0].ID != published.ID {
		t.Fatalf("关注流未返回已关注作者的视频: %+v", following)
	}

	// 私信和个人主页走HTTP层验证主路径。
	var sendResp struct {
		Message struct {
			ID uint `json:"id"`
		} `json:"message"`
	}
	apiPost(t, router, "/message/send", viewerLogin.Token, map[string]any{
		"to_id":   authorLogin.AccountID,
		"content": "hello author",
	}, http.StatusOK, &sendResp)
	if sendResp.Message.ID == 0 {
		t.Fatalf("发送私信响应异常: %+v", sendResp)
	}
	apiPost(t, router, "/profile/getAccountProfile", "", map[string]any{
		"account_id": authorLogin.AccountID,
	}, http.StatusOK, nil)

	// 通知API这里测试历史查询/未读数/标记已读，不依赖SSE长连接。
	notif := &notification.Notification{
		EventID:     "api-smoke-notification",
		RecipientID: viewerLogin.AccountID,
		SenderID:    authorLogin.AccountID,
		Type:        "follow",
		TargetID:    authorLogin.AccountID,
		Content:     "api smoke notification",
	}
	if err := env.db.WithContext(context.Background()).Create(notif).Error; err != nil {
		t.Fatalf("创建通知测试数据失败: %v", err)
	}
	apiPost(t, router, "/notification/list", viewerLogin.Token, map[string]any{}, http.StatusOK, nil)
	var unread struct {
		Count int64 `json:"count"`
	}
	apiPost(t, router, "/notification/unreadCount", viewerLogin.Token, map[string]any{}, http.StatusOK, &unread)
	if unread.Count != 1 {
		t.Fatalf("未读通知数异常: %+v", unread)
	}
	apiPost(t, router, "/notification/markRead", viewerLogin.Token, map[string]any{
		"id": notif.ID,
	}, http.StatusOK, nil)
	apiPost(t, router, "/notification/unreadCount", viewerLogin.Token, map[string]any{}, http.StatusOK, &unread)
	if unread.Count != 0 {
		t.Fatalf("标记已读后未读数应为0: %+v", unread)
	}
}

type apiLoginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    uint   `json:"account_id"`
	Username     string `json:"username"`
}

func apiLogin(t *testing.T, router http.Handler, username string, password string) apiLoginResponse {
	t.Helper()
	var resp apiLoginResponse
	apiPost(t, router, "/account/login", "", map[string]any{
		"username": username,
		"password": password,
	}, http.StatusOK, &resp)
	if resp.Token == "" || resp.RefreshToken == "" || resp.AccountID == 0 {
		t.Fatalf("登录响应异常: %+v", resp)
	}
	return resp
}

func apiPost(t *testing.T, router http.Handler, path string, token string, body any, wantStatus int, out any) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("序列化请求体失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s 状态码异常: got=%d want=%d body=%s", path, rec.Code, wantStatus, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s 响应反序列化失败: %v body=%s", path, err, rec.Body.String())
		}
	}
}

func startAPISmokeWorkers(t *testing.T, env *integrationEnv, ctx context.Context) {
	t.Helper()
	rs := env.repos()

	likeWorker := worker.NewLikeWorker(env.newChannel(t), rs.like, rs.video, rabbitmq.LikeQueue)
	commentWorker := worker.NewCommentWorker(env.newChannel(t), rs.comment, rs.video, rabbitmq.CommentQueue)
	socialWorker := worker.NewSocialWorker(env.newChannel(t), rs.social, rabbitmq.SocialQueue)
	for name, run := range map[string]func(context.Context) error{
		"like":    likeWorker.Run,
		"comment": commentWorker.Run,
		"social":  socialWorker.Run,
	} {
		name := name
		run := run
		go func() {
			if err := run(ctx); err != nil && ctx.Err() == nil {
				t.Logf("%s smoke worker stopped: %v", name, err)
			}
		}()
	}

	// 短暂等待消费者注册，降低刚发布消息时worker尚未开始Consume的概率。
	time.Sleep(100 * time.Millisecond)
}

func TestAPISmokeUnauthorizedProtectedRoute(t *testing.T) {
	env := setupIntegration(t)
	gin.SetMode(gin.TestMode)
	router, _ := apphttp.SetRouter(env.db, env.cache, env.rmq, nil)

	apiPost(t, router, "/like/like", "", map[string]any{
		"video_id": 1,
	}, http.StatusUnauthorized, nil)
}
