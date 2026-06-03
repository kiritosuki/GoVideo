# GoVideo 后续优化路线

> 目标：把当前短视频 feed 后端从“功能可运行”推进到“更接近生产环境、适合写进后端开发简历”的状态。优化重点按顺序是：后端基础结构、MQ 与分布式语义、测试覆盖、前端对接、性能压测、日志监控告警。

## 1. 当前状态

当前项目已经具备短视频系统的核心后端能力：

- 账号注册、登录、JWT 鉴权、刷新 token、退出登录。
- 视频上传、发布、详情、作者作品列表。
- 点赞、取消点赞、评论、删除评论。
- 关注、取关、粉丝列表、关注列表。
- 最新 feed、关注 feed、点赞数榜、热度榜、标签 feed。
- 消息、通知、SSE 推送。
- RabbitMQ 异步处理点赞、评论、关注、热度缓存、timeline、通知。
- Redis 用于限流、缓存、timeline zset、热度窗口、分布式锁。

Redis 目前对 HTTP 接口基本可选：Redis 不可用时，大多数接口会走 DB 或跳过缓存/限流。但 Redis 相关异步派生能力会退化，例如 timeline zset、分钟级热榜、分布式限流不会生效。

RabbitMQ 目前在 API 进程中是可选的，MQ 不可用时点赞、评论、关注会同步写库兜底。但 worker 进程本身仍强依赖 RabbitMQ。

## 2. 当前主要问题

### 2.1 MQ 代码聚合在 router 中

当前 `router.go` 不只负责注册 HTTP 路由，还做了部分 MQ 初始化和后台任务启动：

- 声明 notification 队列。
- 创建 `SSEHub`。
- 启动 `NotificationWorker`。
- 启动 outbox poller。
- 启动 timeline consumer。

这会让 API 进程职责变得混乱。`router` 理想上只负责 HTTP 路由、handler 组装和中间件绑定，不应该承担后台消费和 MQ 拓扑管理。

优化方向：

- 把 MQ 拓扑声明抽到统一的 bootstrap/init 模块。
- 把 worker 启动逻辑移出 `router.go`。
- API 进程只处理 HTTP 请求和维护 SSE 连接。
- worker 进程负责消费 MQ、更新 DB、更新 Redis 派生数据。

### 2.2 NotificationWorker 和 SSEHub 耦合

当前 `NotificationWorker` 会写 `notifications` 表，并直接调用某个 API 实例内存里的 `sseHub.Push`。

单机时这能工作；多 API 实例时会出问题。

例子：

```text
用户 A 连接到 API-1 的 SSE
用户 B 点赞了用户 A 的视频
RabbitMQ 把 notification.like 消息投递给 API-2 上启动的 NotificationWorker
API-2 写入 notifications 表
API-2 调用自己的 sseHub.Push(A)
但 A 的 SSE 连接在 API-1，不在 API-2
结果：数据库有通知，但实时推送失败
```

优化方向：

```text
NotificationWorker
-> 消费 like/comment/social 通知事件
-> 写 notifications 表
-> 发布 notification.push 事件

所有 API 实例
-> 订阅 notification.push
-> 检查本机是否持有目标用户 SSE 连接
-> 持有连接的实例执行 sseHub.Push
```

可选实现：

- Redis Pub/Sub：简单，适合项目展示和轻量部署。
- RabbitMQ fanout/topic：更符合当前 MQ 技术栈。

### 2.3 多 worker 共用同一个 AMQP channel

当前 worker 进程中多个 worker 共用 `rmq.Ch`。RabbitMQ 的 channel 不是用来承载所有并发 worker 的万能共享对象。多个消费者、ack、qos、publish、channel 关闭都混在一个 channel 上，后续排查问题会很困难。

风险：

- 某个 worker 消费异常导致 channel 状态异常，影响其他 worker。
- `Qos` 设置影响同一 channel 上所有消费者，无法给不同队列独立配置预取数量。
- ack/nack、重试、发布重试之间互相干扰，问题定位困难。

优化方向：

- 每类 worker 使用独立 AMQP channel。
- 每个 channel 单独设置 qos。
- worker 退出时只关闭自己的 channel。
- RabbitMQ connection 可以共享，channel 不共享。

建议拆分：

- LikeWorker channel
- CommentWorker channel
- SocialWorker channel
- PopularityWorker channel
- TimelineWorker channel
- OutboxPoller publish channel
- NotificationWorker channel

### 2.4 消息消费幂等不足

RabbitMQ 至少一次投递，不能假设消息只被消费一次。网络抖动、worker 崩溃、ack 失败、手动重试、多 worker 部署都可能导致重复消费。

当前风险：

- 点赞消息重复消费：可能重复增加 `likes_count` 或 `popularity`。
- 评论发布消息重复消费：可能创建重复评论。
- 关注消息重复消费：可能重复插入关注关系。
- 通知消息重复消费：可能重复插入通知。
- timeline 消息重复消费：Redis zset 天然按 videoID 去重，风险较小，但 outbox 状态仍要处理。

例子：

```text
用户 A 评论视频 V
API 发送 comment.publish 消息
CommentWorker 创建评论成功
CommentWorker 准备 ack 时进程崩溃
RabbitMQ 重新投递同一条消息
另一个 CommentWorker 再次创建评论
结果：同一条评论出现两次，视频热度也被加两次
```

优化方向：

- 所有事件都带 `event_id`。
- 增加 `processed_events` 表，字段包括 `event_id`、`event_type`、`processed_at`。
- 消费时先插入 `processed_events`，利用唯一索引保证幂等。
- 插入成功才执行业务逻辑；重复插入失败说明已处理过，直接 ack。
- 对于点赞/关注这类天然有唯一索引的业务，也仍建议记录事件处理状态，避免计数和通知重复。

### 2.5 Outbox 多实例抢占

视频发布使用 outbox 表保证“写视频”和“发 timeline 事件”不分离，这是正确方向。但当前 outbox poller 如果在多个 API 实例中启动，可能同时扫描到同一条 `pending` 消息。

例子：

```text
API-1 和 API-2 同时扫描 outbox_msg where status = pending
两边都查到 video_id = 100
API-1 发布 timeline MQ 成功
API-2 也发布 timeline MQ 成功
同一视频被重复投递到 timeline 队列
```

Redis zset 对同一个 videoID 可以覆盖 member，最终 timeline 可能不重复，但 MQ 消息和处理成本会重复。更重要的是这种模式迁移到其他事件时会有明显风险。

优化方向：

- outbox 增加状态流转：`pending -> processing -> done/failed`。
- 使用数据库事务抢占任务。
- MySQL 8 可考虑 `SELECT ... FOR UPDATE SKIP LOCKED`。
- 或使用原子 update：

```text
update outbox_msg
set status = 'processing'
where id = ? and status = 'pending'
```

只有 `RowsAffected = 1` 的实例才允许发布 MQ。

### 2.6 API 进程和 worker 进程职责边界不清

当前 API 进程里启动了 timeline poller、timeline consumer、notification worker。这样单机开发方便，但分布式部署会产生重复后台任务和本地状态不一致。

建议目标：

```text
API 进程：
- HTTP handler
- JWT 鉴权
- 参数校验
- 调用 service
- 维护 SSE 连接
- 订阅 push 广播事件

Worker 进程：
- LikeWorker
- CommentWorker
- SocialWorker
- PopularityWorker
- TimelineWorker
- OutboxPoller
- NotificationWorker
```

这样 API 可以水平扩容，worker 也可以按队列压力单独扩容。

### 2.7 Redis 不可用时的 MQ 积压

目前 HTTP 接口可以在 Redis 不可用时降级到 DB。但 Redis 派生数据相关的 MQ 仍可能产生积压。

热度缓存例子：

```text
Redis 不可用
API 仍成功初始化 PopularityMQ
用户点赞后 API 发送 video.popularity.update 消息
worker 因 Redis 不可用，没有启动 PopularityWorker
结果：video.popularity.events 队列积压
```

timeline 例子：

```text
用户发布视频
写入 outbox_msg
outbox poller 发布 timeline MQ
Redis 不可用，timeline consumer disabled
结果：timeline 队列积压
/feed/listLatest 仍可通过 MySQL fallback 返回数据
```

优化方向：

- Redis 不可用时，不声明或不发送 Redis 派生数据相关 MQ。
- 或者 worker 正常消费消息，但发现 Redis 不可用时快速 ack 并记录降级日志，避免队列无限积压。
- 更清晰的方案是把 timeline/popularity 视为“可重建缓存”，缓存不可用时不保留消息，恢复后通过 DB 重建。

## 3. 后端结构优化计划

### 3.1 拆分启动层

建议按职责拆分：

```text
cmd/api/main.go
-> load config
-> init db/redis/rabbitmq
-> init repos/services/handlers
-> init router
-> run http server

cmd/worker/main.go
-> load config
-> init db/redis/rabbitmq
-> declare MQ topology
-> start workers
-> graceful shutdown
```

### 3.2 增加应用组装层

可以新增类似：

```text
internal/bootstrap
internal/app
internal/mq
```

职责：

- 统一创建 repo/service/handler。
- 统一声明 MQ exchange/queue/binding。
- 统一管理 worker 启动。
- 避免 `router.go` 越来越大。

### 3.3 明确可选依赖

当前 Redis、RabbitMQ 都有可选语义，但各模块处理方式不完全统一。建议文档化并代码化：

- MySQL：核心依赖，必须可用。
- Redis：可选依赖，不可用时关闭缓存、限流、热度窗口、timeline 加速。
- RabbitMQ：API 进程可选，不可用时同步写库兜底；worker 进程必须可用。

## 4. MQ 优化优先级

### P0：先整理职责边界

- notification MQ 声明移出 `router.go`。
- NotificationWorker 移到 worker 进程。
- timeline poller/consumer 移到 worker 进程。
- API 只保留 SSEHub 和 HTTP notification 查询接口。

### P1：每个 worker 独立 channel

- RabbitMQ connection 可以共享。
- channel 按 worker 类型创建。
- 每个 channel 单独设置 qos。

### P2：事件幂等

- 引入 `processed_events`。
- 所有 MQ event 带 `event_id`。
- worker 消费前做去重。
- 覆盖 like/comment/social/notification/popularity/timeline。

### P3：Outbox 抢占和重试

- outbox 状态从单一 pending 改为 `pending/processing/done/failed`。
- 增加 retry_count、last_error、updated_at。
- 支持失败重试和失败告警。

### P4：SSE 跨实例推送

- NotificationWorker 只负责写表和发布 push event。
- API 实例订阅 push event 后本地推送。
- 支持多 API 实例横向扩容。

## 5. 测试计划

### 5.1 单元测试

优先补这些模块：

- JWT 鉴权：Redis 可用、Redis 不可用、token 被撤销、token 过期。
- Feed：Redis 可用、Redis 不可用、本地缓存命中、DB fallback。
- LikeWorker：重复点赞消息、取消点赞消息、视频不存在。
- CommentWorker：重复 comment.publish 消息、删除不存在评论。
- OutboxPoller：多实例抢占只允许一个实例发布。
- NotificationWorker：重复事件不重复插入通知。

### 5.2 集成测试

建议用 docker-compose 启动 MySQL、Redis、RabbitMQ，测试完整链路：

```text
注册用户
登录
发布视频
outbox -> timeline MQ -> Redis timeline
点赞
like MQ -> likes_count 更新
comment MQ -> comment 创建
notification MQ -> notifications 表
feed 查询
```

### 5.3 分布式场景测试

重点验证：

- 两个 API 实例同时运行时，outbox 不重复发布。
- 两个 worker 实例同时运行时，事件幂等。
- 用户 SSE 连接在 API-1，通知事件由 worker/API-2 处理时，仍能推送到 API-1。
- Redis 不可用时接口走 DB fallback。
- RabbitMQ 不可用时写操作同步落库。

## 6. 前端对接计划

### 6.1 接口字段统一

需要和前端确认：

- `/social/listAllFollowers`
- `/social/listAllVloggers`
- `/profile/getAccountProfile`
- `/feed/listByPopularity`
- `/notification/stream`

### 6.2 热榜 fallback 游标

`/feed/listByPopularity` 同时支持 Redis 热榜分页和 MySQL fallback 分页。

Redis 可用时，前端主要使用：

```text
as_of
offset
```

Redis 不可用时，前端需要保存并回传：

```text
next_latest_popularity -> latest_popularity
next_latest_before     -> latest_before
next_latest_id_before  -> latest_id_before
```

这样 Redis 不可用期间也能连续翻页；Redis 恢复后接口可以重新切回 Redis 热榜分页。

### 6.3 错误码和响应结构

后续应该统一：

- 成功响应结构。
- 错误响应结构。
- 401/403/404/409/429/500 的语义。
- 前端 toast 和错误提示。

## 7. 性能、日志和稳定性

### 7.1 性能测试

先压测核心接口：

- `/feed/listLatest`
- `/feed/listByPopularity`
- `/like/like`
- `/comment/publish`
- `/video/getDetail`

关注指标：

- QPS
- P95/P99 延迟
- MySQL 查询耗时
- Redis 命中率
- MQ 积压量
- worker 消费速率

### 7.2 日志

当前主要使用 `log.Printf`，后续建议替换为结构化日志：

- request_id
- account_id
- route
- status_code
- latency
- mq_event_id
- queue
- retry_count

### 7.3 监控指标

建议暴露 Prometheus 指标：

- HTTP 请求量、错误量、延迟。
- MySQL 查询耗时。
- Redis 命中率、错误数。
- RabbitMQ publish 成功/失败数。
- worker 消费成功/失败数。
- MQ 重试次数、DLX 数量。
- SSE 在线连接数。

### 7.4 告警

建议先做这些告警：

- API 5xx 错误率过高。
- feed P99 延迟过高。
- MQ 队列积压过高。
- worker 消费失败率过高。
- DLX 消息数量增长。
- MySQL 连接池耗尽。
- Redis 不可用。

## 8. 简历表达方向

优化完成后，简历中可以突出这些点：

- 使用 RabbitMQ 解耦点赞、评论、关注、通知和 feed timeline 构建。
- 使用 Outbox Pattern 保证视频发布和 timeline 事件最终一致。
- 使用 Redis zset 构建最新 feed 和分钟级热榜，并提供 MySQL fallback。
- 支持 Redis 可选降级，核心接口在缓存不可用时仍可服务。
- 针对 MQ 至少一次投递实现事件幂等和失败重试。
- 针对多 API 实例 SSE 推送设计跨实例广播机制。
- 通过单元测试、集成测试、压测和监控提升项目生产化程度。

## 9. 推荐实施顺序

1. 整理 MQ 拓扑声明和 worker 启动位置，先把 `router.go` 减负。
2. worker 每类队列使用独立 AMQP channel。
3. 引入事件幂等表，先保护 comment/notification，再覆盖 like/social/popularity。
4. 改造 outbox poller，支持多实例安全抢占。
5. 拆分 NotificationWorker 和 SSEHub，支持跨实例推送。
6. 补核心单元测试和 MQ 集成测试。
7. 对接前端接口字段和错误码。
8. 做 feed、点赞、评论压测。
9. 引入结构化日志、指标和告警。
