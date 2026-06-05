# 集成测试说明

本文档说明 GoVideo 后端集成测试的运行方式、目录结构和覆盖范围。

## 当前结果

最近一次已执行的集成测试结果：

```text
Status: PASS
Passed: 42
Failed: 0
Skipped: 0
Coverage: 42.8%
Cover Packages: ./internal/...
```

这里的覆盖率通过脚本中的 `-coverpkg=./internal/...` 统计，表示测试对后端核心代码包的语句覆盖率，而不是只统计 `tests/integration` 测试包本身。

注意：新增 `api_smoke_integration_test.go` 和 `cos_integration_test.go` 后，需要重新运行脚本更新这里的测试数量和覆盖率。

本轮集成测试曾暴露并修复一个真实业务问题：

- `CommentWorker` 删除评论消息被前置字段校验误丢弃。
- 原因是 `comment.delete` 消息只携带 `CommentID`，不携带 `VideoID/AuthorID`。
- 修复方式是把字段校验下沉到不同 action 的处理函数中：`publish` 走发布字段校验，`delete` 只校验 `CommentID`。

同时修正过一个测试预期问题：

- `ZRemRangeByRank(0, 0)` 删除的是 ZSET 按 score 升序排名最低的 member。
- 因此删除后按倒序查询应剩余高分 member，而不是低分 member。

## 运行前准备

集成测试依赖真实中间件：

- MySQL
- Redis
- RabbitMQ

可以先在 Linux 虚拟机中启动测试中间件：

```bash
docker compose -f deploy/docker-compose.integration.yml up -d
```

测试配置使用：

```bash
configs/config-integration.yaml
```

如果 Go 测试运行在 Mac，而中间件运行在 Linux 虚拟机，配置中的 host 需要写虚拟机 IP。

## 运行命令

集成测试全部带有 build tag：

```go
//go:build integration
```

因此默认 `go test ./...` 不会执行这些测试。

运行集成测试：

```bash
CONFIG_PATH=configs/config-integration.yaml go test -tags=integration ./tests/integration/...
```

也可以使用脚本自动启动远程中间件、运行测试、统计覆盖率并清理环境：

```bash
./scripts/integration-test.sh
```

脚本默认行为：

- 通过 `ssh master` 连接 Linux 虚拟机。
- 复制 `deploy/docker-compose.integration.yml` 到虚拟机的 `~/mysrc/project/GoVideo`。
- 在虚拟机中执行 `docker compose up -d`。
- 等待 MySQL、Redis、RabbitMQ 进入 healthy 状态。
- 在本机运行集成测试。
- 生成 `.run/integration-test.json`。
- 生成 `.run/integration-cover.out`。
- 生成 `.run/integration-cover.txt`。
- 最后执行远程 `docker compose down -v` 清理容器和数据卷。

脚本支持环境变量覆盖：

```bash
SSH_HOST=master \
REMOTE_DIR=~/mysrc/project/GoVideo \
CONFIG_PATH=configs/config-integration.yaml \
COVER_PKG=./internal/... \
HEALTH_TIMEOUT=120 \
./scripts/integration-test.sh
```

只运行某个测试：

```bash
CONFIG_PATH=configs/config-integration.yaml go test -tags=integration ./tests/integration/... -run TestFeedService
```

测试完成后清理容器和数据卷：

```bash
docker compose -f deploy/docker-compose.integration.yml down -v
```

`-v` 会删除 MySQL、Redis、RabbitMQ 的 Docker volume，使下一次测试从干净环境开始。

## 测试目录

当前测试目录：

```text
tests/integration/
  integration_test.go
  testdata_helper_test.go

  api_smoke_integration_test.go
  cos_integration_test.go
  db_schema_integration_test.go

  redis_cache_integration_test.go
  redis_lock_integration_test.go
  rate_limit_integration_test.go

  rabbitmq_integration_test.go
  rabbitmq_retry_dlx_integration_test.go

  account_service_integration_test.go
  video_service_integration_test.go
  feed_service_integration_test.go
  like_service_integration_test.go
  comment_service_integration_test.go
  social_service_integration_test.go
  profile_service_integration_test.go
  message_service_integration_test.go

  like_worker_integration_test.go
  comment_worker_integration_test.go
  social_worker_integration_test.go
  notification_worker_integration_test.go
  notification_subscriber_integration_test.go
  outbox_worker_integration_test.go
  timeline_worker_integration_test.go
  popularity_worker_integration_test.go
```

## 公共初始化

`integration_test.go` 负责：

- 读取 `CONFIG_PATH`。
- 初始化 JWT secret。
- 连接 MySQL。
- 执行 `AutoMigrate`。
- 连接 Redis。
- 连接 RabbitMQ。
- 声明业务 MQ 拓扑。
- 每个测试前后清理数据。

清理范围：

- Redis `FlushDB`。
- RabbitMQ 业务队列和死信队列 `QueuePurge`。
- MySQL 核心业务表 `DELETE`。

## 覆盖范围

### MySQL Schema

覆盖文件：

```text
db_schema_integration_test.go
```

覆盖点：

- likes 唯一索引。
- socials 唯一索引。
- comments.event_id 唯一索引。
- notifications.event_id 唯一索引。
- outbox 状态字段读写。

这些约束是 worker 幂等性的基础。

### Redis 缓存

覆盖文件：

```text
redis_cache_integration_test.go
```

覆盖点：

- `SetBytes/GetBytes/Delete`。
- Redis miss 判断。
- `MGet` 返回顺序。
- ZSET 写入、排序、合并、范围删除。
- Pub/Sub 发布订阅。

### Redis 分布式锁

覆盖文件：

```text
redis_lock_integration_test.go
```

覆盖点：

- 获取锁。
- 重复获取失败。
- token 不匹配不能释放锁。
- 正确 token 可以释放锁。
- TTL 到期后可重新获取。
- 并发竞争同一把锁时只有一个成功。

### 滑动窗口限流

覆盖文件：

```text
rate_limit_integration_test.go
```

覆盖点：

- 窗口内请求允许。
- 超过阈值拒绝。
- 窗口滑动后恢复。
- 不同 key 互不影响。
- 并发请求下计数稳定。

### RabbitMQ

覆盖文件：

```text
rabbitmq_integration_test.go
rabbitmq_retry_dlx_integration_test.go
```

覆盖点：

- like/comment/social/popularity/timeline/notification MQ 拓扑声明。
- topic routing 正确性。
- 业务消息 JSON 可反序列化。
- 独立 channel 互不影响。
- worker 消费失败后进入 retry。
- 超过最大重试次数后进入 DLX。

重试和 DLX 测试使用真实 `TimelineWorker` 做黑盒验证：关闭 Redis client，让 worker 写 ZSET 失败，从而触发项目中的 `consumeWithRetry` 逻辑。

### Service

覆盖文件：

```text
account_service_integration_test.go
video_service_integration_test.go
feed_service_integration_test.go
like_service_integration_test.go
comment_service_integration_test.go
social_service_integration_test.go
profile_service_integration_test.go
message_service_integration_test.go
```

覆盖点：

- account:
  - 注册密码加密。
  - 登录写 DB token。
  - 登录写 Redis token/refreshToken。
  - refreshToken Redis 命中和 Redis miss 降级 DB。
  - rename 更新 token。
  - change password。
  - logout 删除缓存。

- video:
  - publish 写 videos。
  - publish 同事务写 outbox。
  - tag 提取和去重。
  - GetDetail Redis miss 查 DB 并回填缓存。
  - GetDetail Redis hit 返回缓存。
  - cache nil 时直接查 DB。

- feed:
  - `GetVideosByIDs` 保持传入 ID 顺序。
  - L1 本地缓存命中。
  - L2 Redis 命中。
  - L3 MySQL 回源。
  - Redis nil 时仍可使用本地缓存和 DB。
  - `ListLatest` timeline 为空时从 MySQL 重建 ZSET。
  - `ListByPopularity` 合并分钟级热榜。
  - `ListByFollowing` 查询关注流。
  - `buildFeedVideos` 填充点赞状态。

- like/comment/social:
  - MQ 可用时只投递消息。
  - MQ 不可用时走同步 fallback。
  - comment fallback 生成 `mock:` event_id。
  - 参数和权限校验。

- profile:
  - 聚合视频数、总获赞、粉丝数、关注数。

- message:
  - 发送消息。
  - 空内容拒绝。
  - 查询会话消息。

### Worker

覆盖文件：

```text
like_worker_integration_test.go
comment_worker_integration_test.go
social_worker_integration_test.go
notification_worker_integration_test.go
notification_subscriber_integration_test.go
outbox_worker_integration_test.go
timeline_worker_integration_test.go
popularity_worker_integration_test.go
```

覆盖点：

- like worker:
  - 点赞落库。
  - 重复点赞幂等。
  - 取消点赞。
  - likes_count/popularity 不重复变化。

- comment worker:
  - 发布评论落库。
  - event_id 幂等。
  - 删除评论。

- social worker:
  - follow 落库。
  - duplicate follow 幂等。
  - unfollow 删除。

- notification worker:
  - like 事件生成通知。
  - event_id 幂等。
  - Redis Pub/Sub 发布。
  - Redis 不可用时 fallback 到本地 hub。
  - 自己操作自己视频不生成通知。

- notification subscriber:
  - Redis Pub/Sub 消息推送到本节点 hub。

- outbox worker:
  - pending 消息投递 timeline MQ。
  - 投递成功后标记 published。
  - 投递失败后重试。
  - 超过重试次数后标记 failed。

- timeline worker:
  - 消费 timeline 消息写入全局 ZSET。
  - 重复 videoID 不产生重复 member。
  - 只保留最新 1000 条。

- popularity worker:
  - 消费热度消息更新分钟级热榜。
  - 正负 change 都能生效。

### API Smoke

覆盖文件：

```text
api_smoke_integration_test.go
```

覆盖点：

- 使用 `httptest` 直接请求 Gin router，不启动真实 HTTP 端口。
- 注册作者和观众两个用户。
- 登录获取 JWT。
- 作者发布视频。
- 观众拉取 feed。
- 观众点赞视频，并等待 LikeWorker 异步落库。
- 观众评论视频，并等待 CommentWorker 异步落库。
- 观众关注作者，并等待 SocialWorker 异步落库。
- 查询关注流。
- 发送私信。
- 查询个人主页。
- 查询通知列表、未读数、标记已读。
- 未登录访问受保护接口返回 401。

这组测试等价于自动化接口主链路测试，但不会经过前端、Nginx、真实网络端口和浏览器。

### COS 对象存储

覆盖文件：

```text
cos_integration_test.go
```

覆盖点：

- 从 `CONFIG_PATH` 加载真实 COS 配置。
- 配置缺失或仍为占位值时自动跳过，避免误请求。
- 创建一个很小的本地文本文件。
- 调用 `UploadFile` 上传到对象存储。
- 校验返回的对象 URL 非空，并包含对象 key。
- 如果配置了 `public_base_url`，校验返回 URL 使用该前缀。
- 调用 `DeleteObject` 删除测试对象。

这组测试只产生一次真实上传和一次真实删除请求，用于验证对象存储基础功能可用，不做大文件上传和频繁请求。

## 暂未覆盖或后续补充

以下场景建议后续继续补：

- SSE 长连接的真实 HTTP 流式响应测试。
- COS 大文件分块上传场景。
- outbox processing 超时回收的快速测试。

其中 outbox processing 超时当前默认是 5 分钟，字段也是 worker 内部配置。为了不修改业务代码迎合测试，目前没有强行压缩超时时间做测试。

## 注意事项

- 集成测试会清空测试数据库中的业务表。
- 不要把 `configs/config-integration.yaml` 指向开发库或生产库。
- 不要在集成测试中使用真实生产 COS bucket。
- 如果测试失败，优先判断是环境问题、测试问题还是业务 bug。
- 如果确认是业务 bug，应先记录和讨论，再修改业务代码。

## 面试表述

该项目的集成测试重点不是简单追求 handler 覆盖率，而是覆盖后端核心链路：

- 真实 MySQL schema 和唯一索引。
- Redis 缓存、ZSET、Pub/Sub、分布式锁和滑动窗口限流。
- RabbitMQ topic 路由、独立 channel、重试和死信队列。
- service 层的缓存回源、MQ 投递和 fallback 逻辑。
- worker 层的异步消费、幂等、outbox 状态机和 Redis 派生数据更新。

可以这样描述：

```text
我为项目补充了基于真实 MySQL、Redis、RabbitMQ 的集成测试环境，
通过 Docker Compose 在测试虚拟机中启动中间件，并用脚本一键完成环境启动、
测试运行、覆盖率统计和环境清理。测试覆盖了多级缓存、分布式锁、限流、
MQ 路由、重试死信、worker 幂等、outbox 状态机和通知 Pub/Sub 推送等核心链路。
```
