# API 接口文档

本文档记录 GoVideo 后端 HTTP API 的请求方式、鉴权要求、请求参数、响应结构和异步行为，便于前端联调和后续接口测试。

## 基础说明

### 请求格式

除文件上传接口外，接口均使用 JSON：

```http
Content-Type: application/json
```

文件上传接口使用：

```http
Content-Type: multipart/form-data
```

### 鉴权方式

需要登录的接口通过请求头传 JWT：

```http
Authorization: Bearer <token>
```

SSE 接口额外支持 query 参数传 token：

```text
/notification/stream?token=<token>
```

### 错误响应

大多数错误响应格式：

```json
{
  "error": "error message"
}
```

常见状态码：

```text
200 成功
400 请求参数错误
401 未登录或token无效
404 资源不存在
409 资源冲突
429 触发限流
500 服务端错误
```

### 异步接口说明

以下接口在 RabbitMQ 可用时主要是投递消息，最终写库由 worker 异步完成：

```text
POST /like/like
POST /like/unlike
POST /comment/publish
POST /comment/delete
POST /social/follow
POST /social/unfollow
```

接口返回成功通常表示事件已接收或 fallback 已处理，不一定表示异步 worker 已完成最终落库。

如果 RabbitMQ 不可用，部分 service 会走同步 fallback，直接操作 MySQL 或 Redis。

## Account

### 注册

```http
POST /account/register
```

鉴权：不需要。

限流：按 IP，每小时 5 次。

请求：

```json
{
  "username": "alice",
  "password": "123456"
}
```

响应：

```json
{
  "message": "account created"
}
```

### 登录

```http
POST /account/login
```

鉴权：不需要。

限流：按 IP，每分钟 10 次。

请求：

```json
{
  "username": "alice",
  "password": "123456"
}
```

响应：

```json
{
  "token": "jwt-token",
  "refresh_token": "refresh-token",
  "account_id": 1,
  "username": "alice"
}
```

说明：

- 登录成功后会把 token 和 refresh token 写入 MySQL。
- Redis 可用时会缓存 token 和 refresh token 映射。

### 刷新 Token

```http
POST /account/refresh
```

鉴权：不需要。

请求：

```json
{
  "refresh_token": "refresh-token"
}
```

响应：

```json
{
  "token": "new-jwt-token",
  "account_id": 1,
  "username": "alice"
}
```

### 修改密码

```http
POST /account/changePassword
```

鉴权：不需要。

请求：

```json
{
  "username": "alice",
  "old_password": "old-password",
  "new_password": "new-password"
}
```

响应：

```json
{
  "message": "password changed successfully"
}
```

### 根据 ID 查询用户

```http
POST /account/findByID
```

鉴权：不需要。

请求：

```json
{
  "id": 1
}
```

响应：

```json
{
  "id": 1,
  "username": "alice",
  "avatar_url": "https://example.com/avatar.jpg",
  "bio": "hello"
}
```

### 根据用户名查询用户

```http
POST /account/findByUsername
```

鉴权：不需要。

请求：

```json
{
  "username": "alice"
}
```

响应：用户对象。

### 重命名

```http
POST /account/rename
```

鉴权：需要。

请求：

```json
{
  "new_username": "new-alice"
}
```

响应：

```json
{
  "token": "new-jwt-token"
}
```

说明：用户名变化后会签发新 token。

### 登出

```http
POST /account/logout
```

鉴权：需要。

响应：

```json
{
  "message": "account logout successfully"
}
```

说明：会删除 MySQL 和 Redis 中的 token / refresh token 信息。

### 上传头像

```http
POST /account/uploadAvatar
```

鉴权：需要。

请求类型：`multipart/form-data`

字段：

```text
file: jpg/jpeg/png/webp，最大 10MB
```

响应：

```json
{
  "avatar_url": "https://bucket.cos.region.myqcloud.com/avatars/1/xxx.jpg"
}
```

说明：

- 文件会先临时落盘。
- 上传到 COS 后删除本地临时文件。
- 成功后更新用户 `avatar_url`。

### 更新资料

```http
POST /account/updateProfile
```

鉴权：需要。

请求：

```json
{
  "avatar_url": "https://example.com/avatar.jpg",
  "bio": "personal bio"
}
```

响应：

```json
{
  "message": "profile updated successfully"
}
```

## Video

### 上传视频文件

```http
POST /video/uploadVideo
```

鉴权：需要。

请求类型：`multipart/form-data`

字段：

```text
file: mp4，最大 200MB
```

响应：

```json
{
  "url": "https://bucket.cos.region.myqcloud.com/videos/1/20260605/xxx.mp4",
  "play_url": "https://bucket.cos.region.myqcloud.com/videos/1/20260605/xxx.mp4"
}
```

说明：仅上传文件并返回 URL，不写入视频表。

### 上传封面

```http
POST /video/uploadCover
```

鉴权：需要。

请求类型：`multipart/form-data`

字段：

```text
file: jpg/jpeg/png/webp，最大 10MB
```

响应：

```json
{
  "url": "https://bucket.cos.region.myqcloud.com/covers/1/20260605/xxx.jpg",
  "cover_url": "https://bucket.cos.region.myqcloud.com/covers/1/20260605/xxx.jpg"
}
```

说明：仅上传文件并返回 URL，不写入视频表。

### 发布视频

```http
POST /video/publish
```

鉴权：需要。

请求：

```json
{
  "title": "video title #go",
  "description": "description #redis",
  "play_url": "https://example.com/video.mp4",
  "cover_url": "https://example.com/cover.jpg"
}
```

响应：

```json
{
  "id": 1,
  "author_id": 1,
  "username": "alice",
  "title": "video title #go",
  "description": "description #redis",
  "play_url": "https://example.com/video.mp4",
  "cover_url": "https://example.com/cover.jpg",
  "create_time": "2026-06-05T12:00:00+08:00",
  "likes_count": 0,
  "popularity": 0
}
```

说明：

- 同一事务写入 `videos`、`outbox_msgs`、`tags`、`video_tags`。
- outbox 后续由 `OutboxWorker` 投递到 timeline MQ。
- timeline MQ 后续由 `TimelineWorker` 写入 Redis 全局时间线 ZSET。

### 按作者查询视频

```http
POST /video/listByAuthorID
```

鉴权：不需要。

请求：

```json
{
  "author_id": 1
}
```

响应：视频数组。

### 查询视频详情

```http
POST /video/getDetail
```

鉴权：不需要。

请求：

```json
{
  "id": 1
}
```

响应：视频对象。

说明：

- Redis 可用时优先查视频详情缓存。
- Redis miss 时使用分布式锁降低缓存击穿风险。
- Redis 不可用时降级查 MySQL。

## Feed

### 最新视频流

```http
POST /feed/listLatest
```

鉴权：软鉴权。可以不传 token；传 token 时会返回当前用户点赞状态。

请求：

```json
{
  "limit": 10,
  "latest_time": 0
}
```

响应：

```json
{
  "video_list": [
    {
      "id": 1,
      "author": {
        "id": 1,
        "username": "alice"
      },
      "title": "video title",
      "description": "description",
      "play_url": "https://example.com/video.mp4",
      "cover_url": "https://example.com/cover.jpg",
      "create_time": 1780000000,
      "likes_count": 10,
      "is_liked": false
    }
  ],
  "next_time": 1780000000000,
  "has_more": true
}
```

说明：

- Redis timeline 可用时优先从 Redis ZSET 获取视频 ID。
- 视频实体通过 L1 本地缓存、L2 Redis、L3 MySQL 获取。
- timeline 为空时会从 MySQL 重建最近视频时间线。
- 热数据不够一页时会拼接冷数据。

### 点赞数排序视频流

```http
POST /feed/listLikesCount
```

鉴权：软鉴权。

请求：

```json
{
  "limit": 10,
  "likes_count_before": 100,
  "id_before": 20
}
```

说明：

- `likes_count_before` 和 `id_before` 必须同时传或同时不传。
- 按 `likes_count desc, id desc` 游标分页。

响应：同 feed 列表结构。

### 热门视频流

```http
POST /feed/listByPopularity
```

鉴权：软鉴权。

请求：

```json
{
  "limit": 10,
  "as_of": 0,
  "offset": 0,
  "latest_popularity": 0,
  "latest_before": "0001-01-01T00:00:00Z",
  "latest_id_before": null
}
```

说明：

- Redis 可用时合并最近 60 个分钟级热度 ZSET。
- Redis 不可用或查询失败时降级 MySQL。
- Redis 模式用 `as_of + offset` 分页。
- MySQL 降级模式用 `latest_popularity + latest_before + latest_id_before` 游标分页。

### 关注流

```http
POST /feed/listByFollowing
```

鉴权：需要。

请求：

```json
{
  "limit": 10,
  "latest_time": 0
}
```

说明：

- 查询当前用户关注作者的视频。
- Redis 可用时会缓存关注流结果。
- Redis miss 时使用分布式锁降低缓存击穿风险。

响应：同 feed 列表结构。

### 标签视频流

```http
POST /feed/listByTag
```

鉴权：软鉴权。

请求：

```json
{
  "tag_name": "go",
  "limit": 10
}
```

响应：

```json
{
  "video_list": []
}
```

## Like

### 点赞

```http
POST /like/like
```

鉴权：需要。

限流：按账号，每分钟 30 次。

请求：

```json
{
  "video_id": 1
}
```

响应：

```json
{
  "message": "like success"
}
```

异步说明：

- MQ 可用时投递 like 事件，由 `LikeWorker` 写入 likes 表并更新视频点赞数和热度。
- 同时投递 popularity 事件，由 `PopularityWorker` 更新 Redis 热榜。
- MQ 不可用时 fallback 同步写 MySQL / Redis。

### 取消点赞

```http
POST /like/unlike
```

鉴权：需要。

限流：按账号，每分钟 30 次。

请求：

```json
{
  "video_id": 1
}
```

响应：

```json
{
  "message": "unlike success"
}
```

### 是否已点赞

```http
POST /like/isLiked
```

鉴权：需要。

请求：

```json
{
  "video_id": 1
}
```

响应：

```json
{
  "is_liked": true
}
```

### 我的点赞视频

```http
POST /like/listMyLikedVideos
```

鉴权：需要。

响应：视频数组。

## Comment

### 发布评论

```http
POST /comment/publish
```

鉴权：需要。

限流：按账号，每分钟 10 次。

请求：

```json
{
  "video_id": 1,
  "content": "hello"
}
```

响应：

```json
{
  "message": "comment published successfully"
}
```

异步说明：

- MQ 可用时投递 comment 事件，由 `CommentWorker` 写入 comments 表并更新视频热度。
- 同时投递 popularity 事件，由 `PopularityWorker` 更新 Redis 热榜。
- MQ 不可用时 fallback 同步写 MySQL，并生成 `mock:` 前缀的 event_id。

### 删除评论

```http
POST /comment/delete
```

鉴权：需要。

限流：按账号，每分钟 10 次。

请求：

```json
{
  "comment_id": 1
}
```

响应：

```json
{
  "message": "comment deleted successfully"
}
```

说明：

- 只有评论作者可以删除评论。
- MQ 可用时投递 delete 事件，由 `CommentWorker` 删除评论。
- MQ 不可用时 fallback 同步删除。

### 列出评论

```http
POST /comment/listAll
```

鉴权：不需要。

请求：

```json
{
  "video_id": 1
}
```

响应：评论数组。

## Social

### 关注

```http
POST /social/follow
```

鉴权：需要。

限流：按账号，每分钟 20 次。

请求：

```json
{
  "vlogger_id": 2
}
```

响应：

```json
{
  "message": "followed successfully"
}
```

异步说明：

- MQ 可用时投递 follow 事件，由 `SocialWorker` 写入 socials 表。
- MQ 不可用时 fallback 同步写库。
- 不能关注自己。

### 取消关注

```http
POST /social/unfollow
```

鉴权：需要。

限流：按账号，每分钟 20 次。

请求：

```json
{
  "vlogger_id": 2
}
```

响应：

```json
{
  "message": "unfollowed successfully"
}
```

### 粉丝列表

```http
POST /social/listAllFollowers
```

鉴权：需要。

请求：

```json
{
  "vlogger_id": 2
}
```

响应：

```json
{
  "followers": [],
  "follower_count": 0
}
```

说明：`vlogger_id` 为 0 时查询当前登录用户的粉丝。

### 关注列表

```http
POST /social/listAllVloggers
```

鉴权：需要。

请求：

```json
{
  "follower_id": 1
}
```

响应：

```json
{
  "vloggers": [],
  "vlogger_count": 0
}
```

说明：`follower_id` 为 0 时查询当前登录用户关注的人。

### 关注数和粉丝数

```http
POST /social/getCounts
```

鉴权：需要。

响应：

```json
{
  "follower_count": 0,
  "vlogger_count": 0
}
```

## Message

### 发送私信

```http
POST /message/send
```

鉴权：需要。

请求：

```json
{
  "to_id": 2,
  "content": "hello"
}
```

响应：

```json
{
  "message": {
    "id": 1,
    "from_id": 1,
    "to_id": 2,
    "content": "hello",
    "is_read": false,
    "created_at": "2026-06-05T12:00:00+08:00"
  }
}
```

### 查询会话消息

```http
POST /message/list
```

鉴权：需要。

请求：

```json
{
  "peer_id": 2
}
```

响应：

```json
{
  "messages": []
}
```

说明：返回当前用户和 `peer_id` 之间最近 50 条消息，按创建时间倒序。

## Profile

### 查询用户主页

```http
POST /profile/getAccountProfile
```

鉴权：不需要。

请求：

```json
{
  "account_id": 1
}
```

响应：

```json
{
  "account": {
    "id": 1,
    "username": "alice",
    "avatar_url": "https://example.com/avatar.jpg",
    "bio": "hello"
  },
  "video_count": 10,
  "total_likes": 100,
  "follower_count": 20,
  "vlogger_count": 5
}
```

## Notification

### SSE 订阅通知

```http
GET /notification/stream?token=<token>
```

鉴权：需要。token 可放 query 或 `Authorization` 请求头。

响应类型：

```http
Content-Type: text/event-stream
```

推送数据格式：

```text
data: {"id":1,"event_id":"xxx","recipient_id":1,"sender_id":2,"type":"like","target_id":10,"content":"点赞了你的视频","is_read":false,"created_at":"..."}
```

说明：

- 连接建立后，后端会阻塞保持 SSE 长连接。
- 30 秒没有消息时会发送 keepalive。
- 分布式场景下，`NotificationWorker` 写 DB 后通过 Redis Pub/Sub 广播，连接所在节点的 subscriber 再推送到本地 SSEHub。

### 通知列表

```http
POST /notification/list
```

鉴权：需要。token 可放 query 或 `Authorization` 请求头。

响应：

```json
{
  "notifications": []
}
```

说明：返回当前用户最近 50 条通知，按创建时间倒序。

### 标记已读

```http
POST /notification/markRead
```

鉴权：需要。

请求：

标记单条：

```json
{
  "id": 1
}
```

标记全部：

```json
{}
```

响应：

```json
{
  "message": "ok"
}
```

### 未读数

```http
POST /notification/unreadCount
```

鉴权：需要。

响应：

```json
{
  "count": 3
}
```

## 限流

当前限流策略：

```text
/account/register      IP维度，每小时5次
/account/login         IP维度，每分钟10次
/like/like             account维度，每分钟30次
/like/unlike           account维度，每分钟30次
/comment/publish       account维度，每分钟10次
/comment/delete        account维度，每分钟10次
/social/follow         account维度，每分钟20次
/social/unfollow       account维度，每分钟20次
```

限流使用 Redis 滑动窗口。Redis 不可用时会跳过限流，避免限流中间件故障影响核心业务。

## 前端联调注意事项

- 登录后需要保存 `token` 和 `refresh_token`。
- 访问受保护接口时带 `Authorization: Bearer <token>`。
- 视频发布前需要先上传视频和封面，拿到 `play_url`、`cover_url` 后再调用 `/video/publish`。
- 点赞、评论、关注是异步处理，接口返回成功后列表数据可能需要短暂延迟或重新拉取。
- SSE 通知建议登录后建立连接，断线后由前端重连。
- 历史通知和未读数通过 `/notification/list`、`/notification/unreadCount` 查询。
