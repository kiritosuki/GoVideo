# 分布式部署与中间件降级说明

本文用于说明 GoVideo 后端在多节点部署时的依赖关系、降级效果和需要关注的问题。

## 1. 推荐部署模型

推荐按 API 进程和 worker 进程拆分部署：

```txt
api-1
api-2
api-3

worker-1
worker-2

MySQL
Redis
RabbitMQ
COS
```

API 进程负责：

- HTTP 接口。
- JWT 鉴权。
- SSE 长连接。
- 文件临时落盘和 COS 上传。
- 在 RabbitMQ 可用时投递异步业务消息。
- 在 RabbitMQ 不可用时执行部分同步降级逻辑。

worker 进程负责：

- 消费点赞、评论、关注消息。
- 消费热度更新消息。
- 扫描 outbox 表并投递 timeline 消息。
- 消费 timeline 消息并更新 Redis 时间线派生数据。

## 2. 共享状态与本地状态

多节点部署时，业务状态应存放在共享中间件中：

- MySQL：主业务数据、通知记录、outbox 表。
- Redis：缓存、限流、分布式锁、实时通知 Pub/Sub、timeline 派生数据。
- RabbitMQ：异步消息队列。
- COS：视频、封面、头像对象存储。

API 节点本地只保留临时状态：

- SSEHub 内存连接表。
- 上传时的 `.run/uploads/...` 临时文件。

上传文件成功写入 COS 后，本地临时文件会删除。因此 API 节点之间不需要共享本地上传目录。

## 3. Redis 依赖与降级效果

Redis 在不同业务中的依赖等级不一样。

### 3.1 可降级的 Redis 场景

以下功能在 Redis 不可用时可以降级：

- JWT token 缓存：降级查询 MySQL。
- 视频详情缓存：降级查询 MySQL。
- feed 视频批量缓存：降级本地缓存和 MySQL。
- 限流：Redis 不可用时默认放行。
- 点赞状态批量缓存：降级查询 MySQL。
- 分布式锁：Redis 不可用时跳过锁，业务仍可继续。

这些场景下 Redis 主要用于性能优化，不是业务正确性的唯一依赖。

### 3.2 强依赖 Redis 的分布式场景

Notification 实时 SSE 推送在分布式场景下强依赖 Redis Pub/Sub。

当前实时通知流程：

```txt
NotificationWorker 消费 RabbitMQ 消息
写入 notifications 表
发布 PushMessage 到 Redis Pub/Sub
所有 API 节点订阅 Redis 频道
只有持有目标用户 SSE 连接的节点会执行 hub.Push
```

如果 Redis 可用，用户无论连接到哪个 API 节点，都可以收到实时推送。

如果 Redis 不可用，NotificationWorker 会降级调用本节点的 SSEHub：

```txt
NotificationWorker -> 本节点 hub.Push
```

该降级只在单节点或用户 SSE 连接恰好在消费消息的节点上可靠。多 API 节点场景下，消息消费节点和用户连接节点可能不是同一个节点，因此实时推送不保证到达。

不过通知已经写入 MySQL，用户通过通知列表接口仍然可以查询历史通知。也就是说：

```txt
Redis 不可用：
实时 SSE 推送可能丢失
通知入库不丢
用户刷新/拉取通知列表可看到
```

## 4. RabbitMQ 依赖与降级效果

RabbitMQ 用于异步化以下业务：

- 点赞/取消点赞。
- 评论发布。
- 关注。
- 热度更新。
- notification 通知生成。
- outbox 到 timeline 的投递。

API 进程中部分业务已经实现 RabbitMQ 不可用时的同步降级：

- 点赞：MQ 投递失败时同步写 MySQL，并更新缓存。
- 取消点赞：MQ 投递失败时同步删除 MySQL，并更新缓存。
- 评论：MQ 投递失败时同步写 MySQL，并更新缓存。
- 关注：MQ 投递失败时同步写 MySQL。

RabbitMQ 可用时，API 只投递事件，worker 异步消费并更新数据。

RabbitMQ 不可用时，API 通过同步路径尽量保证核心业务可用，但以下能力会受到影响：

- notification worker 无法消费通知消息，实时通知生成依赖同步路径之外的事件会受影响。
- outbox worker 无法投递 timeline 消息，Redis timeline 派生数据不会更新。
- 热度异步更新不可用，依赖同步降级路径和后续重建。

worker 进程强依赖 RabbitMQ。worker 中任一长期 worker 返回不可恢复错误时，当前进程会退出，依赖 Docker 或 Kubernetes 的重启策略恢复。

## 5. MySQL 依赖

MySQL 是主数据源，当前业务强依赖 MySQL。

以下数据以 MySQL 为准：

- 账号。
- 视频。
- 点赞关系。
- 评论。
- 关注关系。
- 通知记录。
- outbox 表。

Redis 和 RabbitMQ 的降级逻辑都建立在 MySQL 可用的前提上。

注意：当前 API 进程启动时会执行 `AutoMigrate`。开发环境可以接受，但生产多副本同时启动时不建议每个 API 节点都自动迁移表结构。

推荐生产化方向：

- 把数据库迁移拆成单独任务。
- API 启动只连接数据库，不自动修改 schema。
- 部署时先执行迁移任务，再启动 API 和 worker。

## 6. COS 依赖与降级效果

视频、封面、头像上传已经迁移到 COS。

当前上传流程：

```txt
API 校验 multipart 文件
保存到本地临时路径 .run/uploads/...
调用 COS UploadFile
上传成功后删除本地临时文件
返回 COS URL
```

COS 在上传接口中是强依赖：

- COS 不可用时，上传接口会失败。
- 不会再返回本地 `/static/...` URL。
- 本地文件只作为临时文件，不作为最终存储。

多 API 节点部署时不需要共享 `.run/uploads`，因为最终文件在 COS 中。

## 7. Outbox 与 Timeline

视频发布后会写入 outbox 表，用于异步更新 timeline 派生数据。

Outbox 状态：

```txt
pending
processing
published
failed
```

OutboxWorker 多节点部署时通过状态抢占：

```txt
pending -> processing
```

只有抢占成功的节点会投递 timeline 消息。投递成功后状态改为 `published`，投递失败后最多重试 3 次，超过后置为 `failed`。

processing 状态超时后会回收为 pending，允许其他节点重新处理。

当前 outbox 没有实现严格一次投递。低概率重复投递可以接受，因为下游 timeline 使用 Redis ZSET，按 videoID 作为 member，天然可以覆盖重复写入。

## 8. 消费幂等性

当前幂等策略：

- 点赞：唯一索引保证重复点赞不会重复写入。
- 取消点赞：硬删除重复执行不会破坏业务。
- 关注：唯一索引保证重复关注不会重复写入。
- 评论：通过 `event_id` 唯一索引保证 MQ 重复消费不会重复创建评论。
- 通知：通过 `event_id` 唯一索引保证重复消费不会重复创建通知。
- timeline：Redis ZSET member 使用 videoID，可容忍重复写入。
- popularity：暂未实现严格幂等，重复消费可能造成热度轻微偏差，目前业务上可接受。

## 9. SSE 分布式推送

SSEHub 是每个 API 节点的本地内存结构：

```txt
map[userID][]channel
```

它只保存连接到本节点的用户长连接。

多节点下不能直接让 worker 调用某个节点的 SSEHub，因为 worker 不知道用户连接在哪个 API 节点。因此当前使用 Redis Pub/Sub 广播通知消息：

```txt
worker publish Redis
api-1 subscribe
api-2 subscribe
api-3 subscribe
```

所有 API 节点都会收到消息，但只有有目标用户连接的节点会真正推送。没有连接的节点调用 `hub.Push` 后会直接返回，不会积压消息。

离线通知可靠性由 MySQL notifications 表保证，不由 Redis Pub/Sub 保证。Redis Pub/Sub 只负责在线实时推送。

## 10. 仍需关注的问题

当前已经具备简历项目层面的分布式部署能力，但距离严格生产环境还需要继续补充：

- 数据库迁移任务独立化，移除 API 启动时的自动迁移。
- Redis 高可用和故障恢复。
- RabbitMQ 高可用、死信队列监控和积压报警。
- COS 上传失败重试和超时配置优化。
- 统一结构化日志。
- 健康检查和 readiness/liveness probe。
- 关键指标监控：接口耗时、错误率、MQ 积压、worker 消费速率、Redis 命中率、COS 上传耗时。
- popularity worker 的严格幂等可作为可选优化。
- outbox failed 状态的人工重试或后台补偿工具。

## 11. 当前部署结论

当前项目可以按以下方式进行分布式部署：

```txt
多个 API 副本
多个 worker 副本
共享 MySQL
共享 Redis
共享 RabbitMQ
共享 COS
```

核心业务数据以 MySQL 为准，文件以 COS 为准，异步事件通过 RabbitMQ 解耦，缓存和实时推送通过 Redis 支撑。

需要特别说明的是：

- Redis 对普通缓存是可降级依赖。
- Redis 对 notification 跨节点实时推送是关键依赖。
- RabbitMQ 对 API 核心写操作有部分同步降级。
- RabbitMQ 对 worker 异步处理是强依赖。
- COS 对上传接口是强依赖。
- MySQL 是系统主依赖，不可降级。
