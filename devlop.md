# GoVideo 后续开发记录

> 这份文档用于记录项目还原后的后续优化方向。当前项目主体逻辑基本按原作者代码还原，后续优先处理后端代码结构、消息队列语义和分布式部署兼容问题。

## 1. 后端开发与优化

### 1.1 MQ 拓扑声明位置

当前 exchange、queue、binding、DLX 的声明逻辑分散在不同位置。

后续可以统一整理：

- API 启动时声明需要发布消息的 exchange/queue。
- Worker 启动时声明自己消费所需的 queue/binding。
- notification 队列声明从 router 中移出，放到统一 MQ 初始化逻辑里。

## 2. 分布式部署 TODO

这些事项主要用于多 API 实例、多 worker 实例部署时避免重复消费、重复推送和实例间状态不一致。

### 2.1 事件消费幂等

问题：MQ 可能重投递，多 worker 实例也可能并发消费，业务逻辑不能假设消息只执行一次。

优化方向：

- 为事件增加 `event_id`。
- 消费前先做事件去重。
- 已处理事件不再重复执行业务逻辑。

重点保护：

- `likes_count +1/-1`
- `popularity +1/-1`
- `notifications` 重复插入
- `comment` 重复创建
- `social` 重复关注

### 2.2 Outbox 多实例抢占

问题：多个 API 实例同时扫描 `outbox_msg pending`，可能抢到同一条 outbox 消息。

优化方向：

- 增加状态流转：`pending -> processing -> done`。
- 或者使用数据库行锁：`SELECT ... FOR UPDATE SKIP LOCKED`。
- 确保同一条 outbox 只会被一个实例发布到 MQ。

### 2.3 Worker 独立部署

目标：API 进程只处理 HTTP 请求和 SSE 连接，后台消费逻辑迁移到 worker 进程。

worker 进程负责：

- like worker
- comment worker
- social worker
- popularity worker
- timeline consumer
- outbox poller
- notification DB worker

### 2.4 SSE 跨实例推送

问题：当前 `sseHub` 只保存本 API 实例上的连接。多 API 实例时，某个用户连接在哪台机器上是不确定的。

优化方向：增加广播层。可选方案：

- Redis Pub/Sub
- RabbitMQ fanout/topic

推荐流程：

```text
NotificationWorker 写 notifications 表
-> 发布 push event
-> 所有 API 实例收到 push event
-> 只有持有目标用户 SSE 连接的实例执行 sseHub.Push
```

### 2.5 NotificationWorker 拆职责

问题：竞争消费 MQ 的 `NotificationWorker` 不应该直接依赖某个 API 实例本地的 `sseHub`。

优化方向：

```text
NotificationWorker:
消费 notification.like/comment/social
写 notifications 表
发布 push event

API 实例:
维护 SSE 连接
接收 push event
调用 sseHub.Push
```

### 2.6 每类 worker 使用独立 AMQP channel

问题：多个 worker 共用同一个 RabbitMQ channel 时，QoS、channel 关闭、ack、publish 可能互相影响。

优化方向：

- LikeWorker 使用独立 channel
- CommentWorker 使用独立 channel
- SocialWorker 使用独立 channel
- PopularityWorker 使用独立 channel
- TimelineWorker 使用独立 channel
- NotificationWorker 使用独立 channel

## 3. 前端对接与部署

### 3.1 social 和 profile 接口命名

当前后端路由里有两个 social 列表接口：

- `POST /social/listAllFollowers`
- `POST /social/listAllVloggers`

前端需要确认调用路径和字段名是否与这两个接口保持一致。

`getProfile` 接口也有变动，当前改为：

- `POST /profile/getAccountProfile`

### 3.2 feed/listByPopularity 游标参数

`/feed/listByPopularity` 同时支持 Redis 热榜分页和 MySQL 降级分页。

正常情况下：

- Redis 可用时，主要走 Redis 热榜分页。
- MySQL 的主要作用是 Redis 宕机时兜底。
- 如果 Redis 恢复，后端会尝试重新刷新 `as_of`，回到 Redis 分页。

如果前端希望在 fallback 期间连续翻 MySQL，需要保存并回传后端返回的三个字段：

```text
next_latest_popularity -> latest_popularity
next_latest_before     -> latest_before
next_latest_id_before  -> latest_id_before
```

即便前端保存并回传这三个字段，后端每次请求也会先尝试 Redis 热榜分页。只有 Redis 不可用时，才会使用这三个字段继续 MySQL fallback 分页。这样 Redis 一旦恢复，接口可以立刻切回热榜分页，相当于具备一定的自愈能力。

如果不保存这三个字段，Redis 不可用期间每次 fallback 都会从 MySQL 降级查询的起点重新开始，无法连续向后翻页。

### 3.3 部署相关

以下内容可以等后端核心逻辑和分布式语义稳定后再处理：

- Docker / docker-compose / Kubernetes 部署细节
- 前端页面体验优化

## 4. 后期稳定性建设

以下内容等核心功能、接口对接和分布式部署方案稳定后再做：

- 日志
- 指标
- 链路追踪
- 告警
- 压测
- 容量规划
