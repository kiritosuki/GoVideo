# GoVideo 面试 QA 复习稿

这份文档按面试官追问方式组织。每个问题下面的“回答”尽量写成可以直接口述的版本；“追问展开”用于面试官继续深挖时补充细节。

## 1. 项目整体

### Q1：简单介绍一下 GoVideo 这个项目？

回答：

GoVideo 是我用 Gin + Gorm 实现的短视频 Feed 流后端系统，支持用户注册登录、视频发布、Feed 流、热门榜、点赞评论、关注关系、消息通知和对象存储上传。

项目的重点不是简单 CRUD，而是围绕后端生产化场景做了一些设计：用 Redis + 本地缓存 + MySQL 做多级缓存，用 RabbitMQ 和 worker 进程处理点赞、评论、关注、热度更新等异步任务，用 Outbox 模式保证视频发布后时间线投递的可靠性，用事件 ID 和唯一索引处理消息重复消费，用 SSE + Redis Pub/Sub 解决分布式通知推送问题，并补充了单元测试、集成测试和 k6 性能测试。

追问展开：

```text
技术栈：Gin、Gorm、MySQL、Redis、RabbitMQ、腾讯云 COS、SSE、k6
API 进程：HTTP 接口、JWT 鉴权、SSE 长连接、文件上传、MQ 投递
Worker 进程：消费 MQ、更新 DB、更新 Redis 派生数据
主数据源：MySQL
缓存/派生数据：Redis
异步解耦：RabbitMQ
文件存储：COS
```

### Q2：这个项目相比普通 CRUD 的亮点在哪里？

回答：

我认为主要有五个亮点。

第一是 Feed 流不是简单查表，而是把最新流拆成 Redis 热数据和 MySQL 冷数据，Redis ZSet 只维护最新 1000 条时间线，冷数据从 MySQL 分页查询。

第二是多级缓存，视频实体查询走本地缓存、Redis、MySQL，视频详情用 Redis 分布式锁和短暂轮询避免缓存击穿。

第三是消息队列异步化，点赞、评论、关注、热度更新、通知生成、时间线更新都通过 RabbitMQ 和 worker 解耦。

第四是可靠性和幂等，视频发布使用 Outbox 状态机保证本地事务和 MQ 投递之间的可靠性，评论和通知使用 event_id 唯一索引避免重复消费。

第五是分布式通知推送，SSEHub 是本地内存连接表，多 API 节点下通过 Redis Pub/Sub 广播通知，让真正持有用户连接的节点完成推送。

### Q3：项目整体怎么部署？

回答：

我把它拆成 API 进程和 worker 进程。API 进程负责 HTTP 请求、JWT 鉴权、SSE 长连接、文件上传和 MQ 投递；worker 进程负责消费点赞、评论、关注、热度、时间线等消息。

多节点部署时，可以是多个 API 副本和多个 worker 副本，共享 MySQL、Redis、RabbitMQ 和 COS。MySQL 保存主业务数据，Redis 保存缓存、限流、分布式锁、热榜、时间线和 Pub/Sub，RabbitMQ 做异步消息队列，COS 做对象存储。

追问展开：

```text
api-1 api-2 api-3
worker-1 worker-2
MySQL Redis RabbitMQ COS
```

API 本地只保留 SSE 连接表和上传临时文件，不把本地磁盘作为最终存储。

## 2. 用户注册登录与鉴权

### Q4：用户注册流程是什么？

回答：

注册接口接收用户名和密码，service 层使用 bcrypt 对密码做哈希，然后写入 MySQL 的 accounts 表。密码不会明文存储。

如果用户名重复，MySQL 唯一索引会报 duplicate key，当前接口会返回错误。更生产化一点可以把 duplicate 错误转换成 409 Conflict 和更明确的“用户名已存在”。

追问展开：

```text
POST /account/register
handler 解析参数
service bcrypt.GenerateFromPassword
repo 插入 accounts
```

### Q5：为什么用 bcrypt？

回答：

bcrypt 自带 salt，并且计算成本可调。相比普通哈希，它能增加暴力破解成本。即使数据库泄露，攻击者也不能直接拿到明文密码。

### Q6：登录流程是什么？

回答：

登录时先根据用户名查 MySQL，然后用 bcrypt 校验密码。密码正确后生成 access token 和 refresh token，并把这两个 token 写入 MySQL。Redis 可用时，还会把 access token 和 refresh token 映射缓存到 Redis。

缓存结构主要是：

```text
account:{accountID} -> access token
account:{accountID}:refresh -> refresh token
refresh:{refreshToken} -> accountID
```

这样做的好处是鉴权时可以优先查 Redis，Redis 不可用时查 MySQL 兜底。

### Q7：你的 JWT 是纯无状态的吗？

回答：

不是纯无状态。JWT 本身包含用户 ID、用户名、过期时间和签名，但服务端还会保存当前有效 token。鉴权时先解析 JWT，再去 Redis 或 MySQL 校验这个 token 是否仍然是当前有效 token。

这样可以支持登出、改名后重新签发 token、单点登录和 token 主动失效。如果是纯 JWT，只要 token 没过期，服务端很难主动让它失效。

### Q8：这个系统是不是单点登录？

回答：

是偏单点登录。因为一个账号在 MySQL 和 Redis 中只保存一份当前有效 token。用户第二次登录后，会覆盖旧 token。旧 token 虽然 JWT 签名可能仍然合法，但和服务端保存的当前 token 不一致，鉴权时会被拒绝。

### Q9：SoftJWTAuth 是什么？

回答：

有些接口允许匿名访问，但用户登录时需要返回个性化字段，比如 Feed 里的 `is_liked`。所以我实现了软鉴权。

没有 token 时直接放行，业务里 accountID 视为 0；有 token 且合法时写入 accountID；有 token 但非法时返回 401。

用在全局 Feed、热门榜、标签 Feed 这类接口上。

## 3. 数据库设计与索引

### Q10：核心表有哪些？

回答：

核心表主要有 accounts、videos、likes、comments、socials、notifications、outbox_msgs、tags、video_tags。

accounts 存用户和 token；videos 存视频元信息；likes 存点赞关系；comments 存评论；socials 存关注关系；notifications 存通知；outbox_msgs 存视频发布后的待投递事件；tags 和 video_tags 用于视频标签。

### Q11：你设计了哪些重要索引？

回答：

videos 表上有 create_time 索引用于最新 Feed；有 likes_count 和 id 的组合索引用于点赞榜；有 popularity、create_time、id 的组合索引用于热门榜 MySQL 降级查询。

likes 表需要 video_id、account_id 组合唯一索引，既用于查询点赞状态，也用于点赞幂等。

socials 表需要 follower_id、vlogger_id 组合唯一索引，用于关注关系查询和关注幂等。

comments 表有 event_id 唯一索引，用于评论消息幂等。

notifications 表有 event_id 唯一索引，用于通知消息幂等。

outbox_msgs 表有 status 索引，用于 outbox worker 扫描 pending 消息。

### Q12：为什么热门榜需要 popularity、create_time、id 复合索引？

回答：

热门榜 MySQL 降级时排序是：

```sql
ORDER BY popularity DESC, create_time DESC, id DESC
```

分页游标也是用 popularity、create_time、id 三个字段。只用 popularity 不够，因为很多视频热度可能相同；再加 create_time 可以让新视频优先；最后用 id 做稳定兜底，保证分页顺序确定，避免重复和漏数据。

### Q13：为什么分页不用 offset？

回答：

浅分页可以用 offset，比如 Redis 热榜合并结果里当前用了 offset。但 MySQL 深分页更适合游标分页。因为 offset 越大，MySQL 要跳过的数据越多，性能会下降。

所以最新 Feed 用 create_time 游标，点赞榜用 likes_count + id，热门榜 MySQL 降级用 popularity + create_time + id，保证分页稳定。

## 4. Feed 流设计

### Q14：你如何设计 Feed 流？

回答：

我把 Feed 分成多个场景：全局最新流、热门榜、关注 Feed、标签 Feed。

全局最新流用 Redis ZSet 保存最近 1000 条热数据，member 是 videoID，score 是视频创建时间。请求在热区时先从 Redis ZSet 查 videoID，再走多级缓存拿视频实体；请求落到冷区时降级查 MySQL。

热门榜用 Redis 维护分钟级热度窗口，查询时合并最近 60 分钟的 ZSet 形成小时级临时榜。Redis 不可用时降级 MySQL 按 popularity、create_time、id 查询。

关注 Feed 通过 social 表查当前用户关注的人，再查这些作者的视频，并做缓存。

### Q15：全局最新 Feed 为什么用 Redis ZSet？

回答：

最新流天然按时间排序，Redis ZSet 很适合这种场景。score 存 create_time，member 存 videoID，可以用 ZREVRANGEBYSCORE 按时间倒序拿最新视频 ID。

ZSet 查询最近数据性能很好，而且 member 唯一，timeline 消息重复消费时重复 ZADD 不会导致 Feed 里出现重复视频。

### Q16：为什么只保留最新 1000 条？

回答：

短视频访问有明显时间热点，大部分用户刷的是最近发布的视频。把所有视频都放 Redis 会浪费内存，而且维护成本高。

所以我把 Redis 作为热数据层，只保存最新 1000 条；超过这个范围的是冷数据，从 MySQL 查。这样能在内存成本和查询性能之间做平衡。

### Q17：Feed 如何保证稳定分页？

回答：

最新流用时间游标，返回本页最后一条视频的 create_time，下一页请求时查询 create_time 更早的数据。

Redis 热区里用 ZSet score 做时间游标，MySQL 冷区里用 `WHERE create_time < latestBefore ORDER BY create_time DESC`。热门榜 MySQL 降级时用 popularity、create_time、id 复合游标，避免相同热度下分页不稳定。

追问展开：

如果只用 popularity 游标，同分视频可能重复或漏掉；如果只用 offset，深分页性能差且数据变化时容易漂移。所以稳定分页要用和排序字段一致的游标。

### Q18：Feed 热数据不够一页怎么办？

回答：

如果 Redis 热区查出来的视频不足一页，说明请求可能处在热区和冷区边界。我会用当前页最后一个视频的 create_time 作为 coldCursor，再去 MySQL 查剩余数量的冷数据进行拼接。

这样不会因为 Redis 只保存最新 1000 条而导致分页中断。

### Q19：Redis 里的 Feed ZSet 空了怎么办？

回答：

Redis ZSet 为空可能是 Redis 重启、缓存被清理或者系统冷启动。此时用 singleflight 保证只有一个请求去 MySQL 查最新 1000 条视频并重建 ZSet，其他并发请求复用这个结果，避免所有请求一起打 MySQL。

重建完成后，再重新走一次正常的 Feed 查询流程。

### Q20：什么是冷热数据？

回答：

热数据是近期高频访问的数据，比如最新发布的 1000 条视频；冷数据是更早的历史视频，访问频率相对低。

我的设计是 Redis 存热数据，MySQL 存全量数据。热数据走 Redis + 多级缓存，冷数据查 MySQL。

### Q21：Feed 流有推模式和拉模式，你选了哪种？

回答：

全局最新流我选的是偏拉模式，用户请求时从 Redis ZSet 或 MySQL 拉取。因为全局最新流对所有用户大体一致，没必要给每个用户提前生成一份收件箱。

关注 Feed 当前也是查询时拉取关注作者的视频，并做响应缓存。对于关注关系很复杂、用户量很大的场景，可以进一步做推拉结合：大 V 用拉模式，普通用户用推模式。

### Q22：推模式和拉模式区别是什么？

回答：

推模式是作者发布视频时，提前把视频 ID 推到粉丝的收件箱里，读的时候很快，但写放大严重，大 V 发布会写大量粉丝 timeline。

拉模式是用户刷 Feed 时，根据关注关系实时查询作者的视频，写入很轻，但读时要做聚合查询，关注人数多时压力更大。

推拉结合是常见方案：普通用户粉丝少，可以推；大 V 粉丝多，发布时不推给所有人，读时再拉取大 V 内容。

### Q23：如果要继续优化 Feed，你会怎么做？

回答：

第一，可以对关注 Feed 做推拉结合，普通作者发布时写入粉丝 inbox，大 V 内容读时拉取。

第二，可以增加推荐特征，比如点赞、评论、发布时间衰减、用户兴趣标签。

第三，可以给 Feed 结果做更细的缓存和预取，比如第一页缓存、热门视频实体批量缓存。

第四，可以监控 Redis 命中率、MySQL 慢查询、Feed P95/P99 延迟，根据瓶颈优化。

## 5. 多级缓存

### Q24：你的多级缓存怎么设计？

回答：

视频实体查询使用 L1 本地缓存、L2 Redis、L3 MySQL。Feed 从 Redis ZSet 或热门榜拿到 videoIDs 后，通过 GetVideosByIDs 批量取视频实体。

流程是先查本地 go-cache，本地未命中再 MGet Redis，Redis 命中后回写本地缓存，Redis 未命中再查 MySQL，MySQL 查询结果异步回写 Redis，并写入本地缓存。

### Q25：为什么要本地缓存？

回答：

本地缓存主要抵挡瞬时热点。比如热门视频在短时间内被大量请求，每个 API 进程可以在内存里缓存几秒，减少 Redis 和 MySQL 压力。

本地缓存 TTL 很短，因为它是每个节点自己的内存，不适合保存太久，否则跨节点一致性差。

### Q26：如何防止缓存击穿？

回答：

视频详情缓存 miss 时，我用 Redis 分布式锁。抢到锁的请求查 MySQL 并回写缓存，没抢到锁的请求短暂等待缓存回写，每 20ms 查一次，最多 5 次。等不到再降级查 MySQL。

Feed 批量视频实体查询中，对同一个 videoID 的 MySQL 查询使用 singleflight 合并，避免同一瞬间大量协程查同一个视频。

### Q27：如何处理缓存穿透？

回答：

当前项目主要处理了缓存击穿，没有完整实现空值缓存或布隆过滤器。对于视频详情，如果不存在会查 MySQL 并返回错误。

如果继续优化，可以对不存在的 videoID 缓存短 TTL 空值，或者用布隆过滤器拦截明显不存在的 ID，避免恶意请求大量不存在 ID 打到数据库。

### Q28：Redis 满了怎么办？

回答：

首先可以通过合理的 TTL 和 Redis 淘汰策略控制内存。项目里视频详情缓存 TTL 是分钟级，本地缓存 TTL 是秒级，热门榜合并结果 TTL 只有 2 分钟。

其次 Feed 全局 timeline 只保留最新 1000 条，不无限增长。热榜按分钟窗口设计，也可以设置过期时间。

如果 Redis 真的内存不足，生产上要看业务选择淘汰策略，比如 volatile-lru 或 allkeys-lru，并监控内存使用和 key 命中率。核心正确性依赖 MySQL，Redis 被淘汰主要影响性能，不影响主数据。

### Q29：Redis 不可用怎么办？

回答：

大部分业务会降级 MySQL。JWT 鉴权查 MySQL token，视频详情查 MySQL，Feed 查 MySQL，热门榜查 MySQL 排序，限流默认放行。

特殊的是分布式 SSE 实时推送，Redis Pub/Sub 不可用时只能降级为本节点本地推送，多 API 节点下实时推送不一定到达。但通知已经写入 MySQL，用户通过通知列表仍能看到。

### Q30：分布式锁和 singleflight 分别怎么用？什么时候用哪个？

回答：

这两个在项目里都有实际使用，但用在不同位置。

```text
VideoService.GetDetail：
  使用 Redis 分布式锁
  解决多个 API 节点同时查同一个视频详情导致的缓存击穿

FeedService.ListByFollowing：
  使用 Redis 分布式锁
  解决关注 Feed 响应缓存 miss 时的跨节点重复回源

FeedService.GetVideosByIDs：
  使用 singleflight
  合并同一个 API 进程内同一个 videoID 的重复 MySQL 查询

FeedService.ListLatest：
  使用 singleflight
  合并本进程内 Redis timeline 重建、冷数据查询、冷热拼接查询
```

具体来说，`/video/getDetail` 是用户可能高频访问的详情接口。如果某个热门视频的 Redis 缓存失效，多台 API 节点可能同时收到请求，所以这里用了 Redis 分布式锁：

```text
lock:video:detail:id={videoID}
```

抢到锁的请求查 MySQL 并回写 Redis；没抢到锁的请求每 20ms 查一次缓存，最多等 5 次；如果还是等不到，就降级查 MySQL，保证接口可用。

`ListByFollowing` 也用了 Redis 分布式锁。因为关注 Feed 的响应缓存 key 和用户、limit、cursor 有关，如果多个节点同时 miss 同一个关注列表缓存，抢到锁的节点查 MySQL 并回写缓存，其他节点等待缓存或降级查 MySQL。

`GetVideosByIDs` 用的是 singleflight。这个函数是 Feed 根据 videoIDs 批量取视频实体，先查本地缓存，再 MGet Redis，最后对 Redis 未命中的视频查 MySQL。这里每个视频 ID 都去 Redis 抢分布式锁成本太高，所以用 singleflight 合并同一个 API 进程内同一个 videoID 的重复查库。

`ListLatest` 里也用了 singleflight，主要用于三个场景：

```text
1. Redis global_timeline 为空时，合并本进程内的 timeline 重建
2. 请求落到冷数据区时，合并相同 cursor 的 MySQL 冷数据查询
3. 热数据不足一页时，合并冷热拼接中的 MySQL 查询
```

需要注意的是，singleflight 只能合并当前进程内的并发请求，不能跨 API 节点。所以在项目里，如果是明确的跨节点缓存击穿风险，比如视频详情和关注 Feed，就使用 Redis 分布式锁；如果是局部查询合并，或者每个 ID 都加分布式锁成本太高，就使用 singleflight。

面试时可以总结成一句话：

> singleflight 解决单进程内的重复工作，Redis 分布式锁解决多节点之间的重复工作。能用 singleflight 的地方优先用它，涉及跨节点缓存击穿时才用分布式锁。

## 6. 热门榜

### Q31：热榜怎么实现的？

回答：

热榜使用 Redis ZSet 保存分钟级热度窗口。每个分钟一个 key，比如 `hot:video:1m:202606061230`，member 是 videoID，score 是热度变化。

查询热门榜时，取过去 60 分钟的分钟级 ZSet，用 ZUNIONSTORE 合并成一个小时级临时榜，按 score 倒序取 videoID，再查视频实体返回。

合并榜设置 2 分钟 TTL，避免每次请求都合并 60 个 ZSet。

### Q32：热度怎么计算？

回答：

当前热度比较简单，点赞、取消点赞、评论会影响热度。点赞加分，取消点赞减分，评论加分。热度既会更新 videos 表的 popularity，也会异步更新 Redis 热榜缓存。

当前项目更关注热榜工程链路，而不是复杂推荐算法。后续可以引入时间衰减、评论权重、完播率等更复杂的热度模型。

### Q33：热榜为什么用分钟窗口？

回答：

分钟窗口能体现近期热度。如果只用全局累计热度，老视频可能长期占据榜单；如果每次都实时扫描数据库，性能成本太高。

分钟窗口把行为按时间切片，查询时合并最近 60 分钟，既能反映近期热度，又能把写入和查询成本控制在 Redis ZSet 上。

### Q34：热榜 Redis 不可用怎么办？

回答：

降级 MySQL，根据 videos 表中的 popularity、create_time、id 排序查询。返回时 asOf 和 offset 置 0，让下次请求仍可以优先尝试 Redis；如果 Redis 恢复，接口能自动回到 Redis 路径。

### Q35：热榜重复消费会不会有问题？

回答：

PopularityWorker 当前没有做严格幂等。重复消费可能造成热度分数轻微偏差，但热度榜是排序指标，不是主业务数据，短期内可以容忍。

如果要优化，可以给 popularity event 加 Redis 去重 key，或者落一张 event 去重表，确保每个 event_id 只处理一次。

## 7. RabbitMQ 与异步化

### Q36：为什么选择 RabbitMQ，而不是 Kafka？

回答：

这个项目里的消息主要是业务事件，比如点赞、评论、关注、通知、时间线更新，特点是需要可靠投递、路由灵活、消费失败重试和死信队列。RabbitMQ 的 topic exchange、routing key、ack、DLX 都比较适合这种业务异步场景。

Kafka 更适合高吞吐日志流、事件流、大规模顺序消费和数据管道，比如埋点日志、行为流、实时计算。它吞吐很强，但业务路由和单条消息失败处理通常没有 RabbitMQ 这么直接。

所以我选择 RabbitMQ，是因为当前系统更偏业务消息队列，而不是海量日志流处理。

### Q37：RabbitMQ 和 Kafka 的核心区别？

回答：

RabbitMQ 是传统消息队列，强调 exchange 路由、队列、ack、重试、死信队列，适合业务解耦和任务队列。

Kafka 是分布式日志系统，消息按 topic/partition 追加写入，消费者按 offset 消费，适合高吞吐、可回放、流式处理。

简单说，RabbitMQ 更像“任务分发系统”，Kafka 更像“可持久化的事件日志”。

### Q38：RabbitMQ 解决了什么问题？

回答：

主要解决三个问题。

第一是解耦，API 不需要直接执行所有写库和派生更新逻辑，只要投递事件。

第二是削峰，点赞、评论、关注等写请求可以快速返回，后台 worker 慢慢消费。

第三是扩展，像通知队列可以绑定到点赞、评论、关注 exchange 上，新增通知逻辑不需要改主业务 worker。

### Q39：它具体解耦了什么？

回答：

以点赞为例，点赞接口只负责校验用户和投递 like.like 事件。LikeWorker 负责写 likes 表和更新视频点赞数；NotificationWorker 订阅同一个 like.like 事件生成通知；PopularityWorker 通过热度事件更新热门榜。

这样点赞接口不需要直接耦合“写点赞表、更新计数、生成通知、更新热榜”所有逻辑。

### Q40：完整 MQ 链路是什么？

回答：

以评论发布为例：

```text
用户调用 /comment/publish
CommentService 生成 CommentEvent
发布到 comment.events exchange，routing key = comment.publish
comment.events queue 收到消息
CommentWorker 消费，写 comments 表，更新视频热度
notification.comment queue 也收到同一事件
NotificationWorker 消费，写 notifications 表，并推送 SSE
如果消费失败，按 x-retry-count 重试
超过 3 次进入 comment.events.dlx 或 notification.comment.dlx
```

### Q41：RabbitMQ 如何保证消息不丢？

回答：

生产侧，exchange 和 queue 都是 durable，发布消息时 DeliveryMode 设置为 Persistent，表示消息持久化。

消费侧，使用手动 ack。只有业务处理成功才 Ack；失败时重试；超过重试次数后 Nack 到死信队列。

对于视频发布这种本地事务和 MQ 投递一致性问题，我没有直接在业务事务里发 MQ，而是使用 Outbox。先在 MySQL 事务里写 videos 和 outbox，再由 OutboxWorker 后台投递 MQ。

追问展开：

严格说，还可以继续增强 publisher confirm，当前项目主要靠 Outbox 和持久化队列提升可靠性。Outbox 能处理“业务数据已写入但 MQ 投递失败”的问题。

### Q42：消费失败怎么处理？

回答：

所有 worker 复用统一的 consumeWithRetry。process 返回 error 表示业务处理失败。失败后读取消息 header 里的 x-retry-count，如果没超过 3 次，就把消息重新 publish 回原 exchange 和 routing key，并把 retry-count 加 1，然后 ack 原消息。

如果超过 3 次，就 Nack 且 requeue=false，让消息进入死信队列。

### Q43：为什么不直接 Nack requeue=true？

回答：

直接 requeue=true 会让消息马上回到队列，但不好控制重试次数，可能形成无限重试。手动 republish 可以在 header 里记录 retry-count，超过次数后进入死信队列，便于排查和补偿。

### Q44：重复消费如何避免？

回答：

我不假设 MQ 是 exactly-once，而是在消费端做幂等。

点赞和关注用业务唯一索引，重复插入直接忽略；取消点赞和取消关注是硬删除，重复执行不影响结果；评论和通知用 event_id 唯一索引，重复 event_id 插入失败后直接返回 nil；timeline 使用 Redis ZSet，videoID 作为 member，重复 ZADD 不会产生重复视频。

### Q45：消息进入死信队列后怎么办？

回答：

当前项目会把超过重试次数的消息放入对应的 `.dlx` 死信队列，便于后续排查。生产环境下一般会加死信队列监控和报警，提供人工重试或后台补偿工具。

当前项目已经有死信队列机制，但还没有做死信消息的自动补偿后台，这是后续优化方向。

## 8. Outbox 模式

### Q46：为什么要引入 Outbox？

回答：

视频发布后，需要同时写 videos 表和通知 timeline 更新 Redis 时间线。如果直接写库后发 MQ，会出现写库成功但 MQ 投递失败的问题，导致 MySQL 有视频但 Feed 时间线没有这个视频。

Outbox 的做法是：在同一个 MySQL 事务里写 videos 和 outbox 消息。事务成功后，OutboxWorker 后台扫描 outbox 表，把 pending 消息投递到 MQ。这样至少能保证“只要视频写入成功，就一定有一条待投递消息留在数据库里”。

### Q47：为什么视频发布用了 Outbox，而不是直接投递 MQ？

回答：

因为视频发布和点赞、关注这类操作的接口语义不一样。

点赞、关注这类接口通常只需要告诉前端“操作已接收”或“操作成功”，前端不强依赖后端立刻返回一个新资源 ID。所以这类操作可以更容易地异步化：API 投递 MQ，worker 后台消费写库；MQ 不可用时再走同步 fallback。

但视频发布接口不一样。用户发布视频后，前端通常需要立刻拿到确定的视频 ID、作者信息、播放 URL、封面 URL、发布时间等数据，用于跳转详情页、展示发布结果或更新个人主页。如果 API 什么都不写库，只投递一条 MQ，让 worker 后台再插入 videos 表，那么接口返回的语义就会变成：

```text
/video/publish 返回成功
= 发布请求已接收
≠ 视频已经发布成功
≠ 数据库里已经有 video_id
```

这样前端拿不到确定的 video_id，后端还需要额外设计 task_id、发布状态表、轮询接口或通知机制来告诉用户视频到底有没有发布成功。

所以当前项目选择让视频发布的主业务同步完成：API 请求内直接写入 videos 表、标签关系，并返回确定的视频对象。时间线更新属于发布成功后的派生事件，再通过 Outbox 异步投递给 TimelineWorker。

这时 Outbox 解决的是“主业务已成功，但派生事件不能丢”的问题。如果直接写库后发 MQ，会出现一个典型一致性问题：

```text
1. videos 表插入成功
2. MQ 投递失败，或者投递前进程崩溃
3. MySQL 里已经有视频，但 Redis Feed 时间线永远没有这条视频
```

如果反过来先投 MQ 再写 MySQL，也会有另一个问题：

```text
1. MQ 投递成功
2. videos 表插入失败
3. TimelineWorker 消费到一个不存在的视频 ID
```

所以视频发布使用 Outbox：在同一个 MySQL 事务里同时插入 `videos` 和 `outbox_msgs`。只要事务提交成功，就说明视频主数据和待投递事件都已经落库。即使 RabbitMQ 当时不可用，outbox 消息也不会丢，后续 OutboxWorker 可以继续扫描并重试投递。

所以这和点赞、评论、关注不完全一样。点赞、评论、关注更容易接受异步最终一致，接口不需要返回新创建资源的完整 ID；视频发布则需要同步确认主资源已经创建成功，并立刻返回 video_id 和 URL 等数据。

但视频发布后的 timeline 更新属于短视频系统的核心展示链路。视频主数据已经写入 MySQL 后，如果“更新时间线”的消息丢失，就可能出现数据库中存在视频，但全局 Feed 时间线里没有这条视频，用户刷 Feed 看不到新发布的视频。Redis timeline 为空时可以从 MySQL 重建，但如果只是局部缺失某一条视频，不一定能自动发现和自愈。

所以我对视频发布使用 Outbox，把“视频已发布，需要更新时间线”这个事件和视频主数据放在同一个 MySQL 事务中提交。这样后续即使 MQ、Redis 或 worker 短暂失败，也可以通过 outbox 状态机继续重试和补偿。

面试时可以总结成一句话：

> 点赞、关注这类操作可以只返回操作结果，适合直接异步化；视频发布需要立刻返回确定的 video_id 和资源 URL，所以我同步完成视频主数据入库，再用 Outbox 保证“更新时间线”这个派生事件可靠投递。

### Q48：Outbox 状态机怎么设计？

回答：

我设计了四个状态：

```text
pending：待投递
processing：某个 worker 正在处理
published：已经投递成功
failed：超过重试次数，失败留库
```

OutboxWorker 扫描 pending 消息，先通过条件更新把 pending 改成 processing，抢占成功后投递 MQ。投递成功改成 published，失败就 retry_count +1，未超过 3 次改回 pending，超过后改成 failed。

### Q49：分布式下多个 OutboxWorker 会不会抢同一条消息？

回答：

可能同时扫描到，但只有一个能抢占成功。抢占使用条件更新：

```sql
UPDATE outbox_msgs
SET status = 'processing'
WHERE id = ? AND status = 'pending'
```

只有 RowsAffected=1 的节点处理这条消息，其他节点 RowsAffected=0，会放弃。

### Q50：processing 状态的消息卡住怎么办？

回答：

如果 worker 抢占成功后崩溃，消息可能一直停在 processing。为了解决这个问题，OutboxWorker 每次扫描前会把 updated_at 超过 5 分钟的 processing 消息重置为 pending，让其他节点重新抢占处理。

### Q51：Outbox 能保证 exactly-once 吗？

回答：

不能。Outbox 通常保证 at-least-once。比如 worker 已经投递 MQ 成功，但还没来得及把状态改成 published 就崩溃，超时后这条 outbox 可能重新投递。

当前下游 timeline 用 Redis ZSet，member 是 videoID，重复 ZADD 不会产生重复视频，所以可以接受。如果下游不能容忍重复，就需要在下游做更严格的 event_id 幂等。

### Q52：为什么不让 OutboxWorker 直接更新 Redis，还要投 MQ？

回答：

直接更新 Redis 也能工作，但会把“扫描 outbox”和“更新 timeline 派生数据”耦合在一个 worker 里。

加 MQ 后职责更清晰：OutboxWorker 只负责把数据库里的待投递事件可靠投递出去，TimelineWorker 只负责消费 timeline 事件并更新 Redis。后续如果视频发布事件还要被其他消费者使用，比如推荐系统、审核系统，就可以继续绑定新队列，不需要改 OutboxWorker。

## 9. worker 幂等

### Q53：点赞 worker 怎么保证幂等？

回答：

likes 表对 video_id 和 account_id 建唯一索引。LikeWorker 插入点赞关系时，如果 duplicate，就说明用户已经点过赞，直接返回 nil，不再增加 likes_count 和 popularity。

取消点赞是硬删除，只有 RowsAffected > 0 才扣减点赞数，重复删除不会重复扣减。

### Q54：评论 worker 怎么保证幂等？

回答：

评论不能用 userID + videoID 幂等，因为同一个用户可以对同一个视频发多条评论。所以我在 CommentEvent 里生成 event_id，并在 comments 表上对 event_id 建唯一索引。

重复消费同一个事件时，插入 comments 会触发 duplicate，worker 直接返回 nil 并 ack 消息，不会重复创建评论，也不会重复增加热度。

### Q55：关注 worker 怎么保证幂等？

回答：

socials 表对 follower_id 和 vlogger_id 建唯一索引。重复关注会 duplicate，worker 忽略即可。取消关注是硬删除，重复删除不影响最终结果。

### Q56：通知 worker 怎么保证幂等？

回答：

notifications 表有 event_id 唯一索引。NotificationWorker 根据点赞、评论、关注事件构造通知时，会沿用事件的 event_id。重复消费时插入通知表 duplicate，直接返回 nil，避免重复通知入库。

### Q57：TimelineWorker 怎么保证幂等？

回答：

TimelineWorker 更新 Redis ZSet，member 是 videoID。ZSet 的 member 唯一，同一个 videoID 重复 ZADD 不会产生多条 Feed 记录，只会覆盖 score。因此 timeline 重复消息不会让 Feed 出现重复视频。

### Q58：PopularityWorker 为什么没做严格幂等？

回答：

热度榜是排序指标，不是主业务强一致数据。重复消费可能造成热度分轻微偏差，但不会破坏点赞、评论、关注这些主数据。当前项目把它作为可接受误差。

如果要进一步优化，可以给 popularity event 做 Redis 去重 key 或 event 去重表，保证同一个 event_id 只更新一次热度。

## 10. SSE 通知推送

### Q59：SSE 是怎么工作的？

回答：

SSE 是 Server-Sent Events，本质是一个 HTTP 长连接。客户端建立连接后，服务端保持请求不返回，有新消息时按 `data: ...\n\n` 格式写入响应流，并 flush 到客户端。

我的 SSEHandler 会为当前用户创建一个 channel，放入 SSEHub 的 `map[userID][]channel` 中。NotificationWorker 或 Subscriber 调用 hub.Push 时，会把通知写入对应用户的 channel，SSEHandler 从 channel 读到消息后写回客户端。

### Q60：为什么用 SSE，不用 WebSocket？

回答：

当前通知场景主要是服务端向客户端单向推送，客户端不需要通过同一个连接频繁发送消息。SSE 基于 HTTP，实现简单，浏览器支持好，也适合通知推送。

WebSocket 更适合双向实时通信，比如聊天、在线协作、游戏。如果后续做实时聊天，可以考虑 WebSocket。

### Q61：SSEHandler 里为什么是 for 循环阻塞？

回答：

这是 SSE 的正常工作方式。SSE 是长连接，Handler 处理这个 HTTP 请求时不会立即返回，而是在请求 goroutine 中循环等待通知、写入响应流。如果客户端断开，请求 context 会取消，Handler 退出并取消订阅。

### Q62：分布式部署下 SSE 有什么问题？

回答：

SSEHub 是每个 API 节点自己的本地内存连接表。如果用户连接在 api-1，但 NotificationWorker 在 api-2 消费到通知并调用 api-2 的 hub.Push，api-2 本地没有这个用户连接，消息就推不出去。

所以多节点下不能让 worker 直接依赖某一个本地 SSEHub。

### Q63：你如何解决分布式通知推送？

回答：

我使用 Redis Pub/Sub。NotificationWorker 消费 MQ 后，先把通知写入 MySQL，然后把 PushMessage 发布到 Redis 频道。所有 API 节点都订阅这个频道，收到消息后调用本节点 hub.Push。

只有真正持有目标用户 SSE 连接的节点能推送成功；其他节点没有这个用户的连接，hub.Push 会直接返回，不会积压。

### Q64：所有节点都收到 Pub/Sub 消息，会不会重复推给用户？

回答：

一般不会。因为用户的 SSE 连接只存在于某个 API 节点的本地 SSEHub。其他节点虽然收到 Pub/Sub 消息，但 clients 里没有这个 userID，Push 直接返回。

如果用户打开多个标签页，同一个节点或多个节点上可能有多个 SSE 连接，这时每个连接收到通知是合理的。

### Q65：用户离线时通知会丢吗？

回答：

实时推送会丢，因为 Redis Pub/Sub 不保存离线消息。但通知不会丢，因为 NotificationWorker 在推送前已经写入 notifications 表。用户重新上线后可以通过通知列表接口拉取历史通知和未读数。

## 11. COS 对象存储

### Q66：为什么接入 COS？

回答：

多 API 节点部署时，本地文件不共享。如果视频上传到 api-1 的本地磁盘，之后请求打到 api-2，api-2 可能访问不到这个文件。

所以我把视频、封面、头像迁移到腾讯云 COS。API 只负责临时接收和上传文件，最终数据库保存 COS URL，所有节点都能访问同一份资源。

### Q67：上传流程是什么？

回答：

前端 multipart 上传文件到 API，API 校验文件大小和后缀，然后保存到 `.run/uploads/...` 临时路径。接着调用 COS SDK 的 UploadFile 上传，上传成功后删除本地临时文件，并返回 COS URL。

### Q68：为什么先落盘，不直接流式上传？

回答：

当前使用 COS SDK 的高级上传接口 Upload，它可以根据文件大小自动选择普通上传或分块上传，适合短视频大文件。这个接口需要本地文件路径，所以先临时落盘。

如果后续想进一步优化，可以做前端直传 COS，后端只负责生成临时凭证或签名，这样可以减轻 API 服务器带宽压力。

## 12. 降级与稳定性

### Q69：Redis 故障时系统还能用吗？

回答：

大部分核心接口还能用。视频详情、Feed、热门榜、JWT 鉴权都可以降级 MySQL。限流在 Redis 故障时默认放行。分布式锁不可用时跳过锁，但业务继续执行。

受影响最大的是分布式 SSE 实时推送，因为跨节点广播依赖 Redis Pub/Sub。Redis 不可用时会降级本节点 hub.Push，单节点可靠，多节点不完全可靠。但通知已经入库，用户可以拉取历史通知。

### Q70：RabbitMQ 故障时怎么办？

回答：

API 层对点赞、评论、关注等核心写操作做了同步 fallback。MQ 可用时投递事件并异步消费；MQ 不可用时，service 会尽量同步写 MySQL，保证主业务可用。

但 worker 异步能力会受影响，比如通知异步生成、时间线派生更新、热度更新会受影响。worker 进程本身强依赖 RabbitMQ，异常时退出，由 Docker 或 Kubernetes 重启。

### Q71：MySQL 故障怎么办？

回答：

MySQL 是主数据源，当前系统强依赖 MySQL。Redis 和 RabbitMQ 的降级逻辑都建立在 MySQL 可用的前提上。

生产环境需要 MySQL 高可用、备份恢复、主从复制、读写分离等方案。当前项目作为简历项目主要做了 Redis 和 MQ 的降级，MySQL 高可用是后续生产化方向。

### Q72：COS 故障怎么办？

回答：

COS 对上传接口是强依赖。COS 不可用时，视频、封面、头像上传会失败，不会返回本地 URL。因为本地文件只是临时文件，不作为最终存储。

历史已上传资源不受影响。

## 13. 性能压测

### Q73：你做过性能压测吗？

回答：

做过。我用 k6 写了性能测试脚本，重点测了四个核心接口：`feed/listLatest`、`feed/listByPopularity`、`video/getDetail`、`comment/publish`。

测试脚本会通过 Docker Compose 启动 MySQL、Redis、RabbitMQ，然后本地启动 API 和 worker，再执行 k6 压测，输出 QPS、P95/P99 延迟和错误率。

### Q74：性能压测重点关注哪些指标？

回答：

接口层面关注 QPS、平均延迟、P95/P99 延迟、错误率。

读接口还要关注 Redis 命中率、MySQL 查询量、慢查询、本地缓存效果。

写接口要关注 RabbitMQ 投递成功率、队列积压、worker 消费速率、死信队列数量、MySQL 写入耗时。

系统层面关注 CPU、内存、网络、数据库连接数、Redis 内存、RabbitMQ queue depth。

### Q75：QPS 多少？

回答：

我本地压测环境不是生产环境，是本机 API 连接虚拟机里的 MySQL、Redis、RabbitMQ，所以结果不能代表真实线上极限。

最开始我用 `20 VU` 无 sleep 压测 listLatest，跑到约 1600+ QPS，但这导致本机到虚拟机 MySQL 的连接资源被打满，后续出现 `can't assign requested address`。这个结果说明压测参数过于激进，也暴露出数据库连接池和远程测试环境的瓶颈。

后来我把默认压测改温和，使用更合理的 VUS、duration 和 sleep，并增加脚本间冷却、失败后停止后续脚本。面试时我会强调这个压测主要用于发现瓶颈和比较优化前后差异，而不是声称线上 QPS。

### Q76：如果要正式压测，你会怎么设计？

回答：

第一，准备接近真实的数据量，比如用户、视频、点赞、评论、关注关系。

第二，分接口压测，不一上来就跑全量。先测单接口基准，再测混合场景。

第三，逐步增加并发，从低 VUS 开始，观察 P95/P99、错误率、MySQL 连接数、Redis 命中率、MQ 积压。

第四，区分冷启动和稳定状态。冷启动看缓存重建能力，稳定状态看缓存命中后的吞吐。

第五，压测结束后分析瓶颈，比如是 Redis、MySQL、MQ、网络还是 API CPU。

### Q77：压测中发现了什么问题？

回答：

我发现无等待循环压测和直觉不一样，20 VU 不是 20 QPS，而是 20 个虚拟用户尽可能快地循环请求。它会把 QPS 打得非常高。

在我的本地环境中，这导致 API 访问虚拟机 MySQL 时连接资源耗尽，出现 `can't assign requested address`。这说明需要设置合理的数据库连接池、压测节奏和监控指标。

## 14. 完整业务链路

### Q78：讲一下用户注册登录的全过程。

回答：

用户注册时，后端接收用户名和密码，使用 bcrypt 加密密码，然后写入 accounts 表。登录时，根据用户名查用户，bcrypt 校验密码，成功后生成 access token 和 refresh token，写入 MySQL，并缓存到 Redis。

后续访问受保护接口时，请求头带 Bearer token。后端先解析 JWT，再去 Redis 或 MySQL 校验这个 token 是否是当前有效 token，校验通过后把 accountID 和 username 写入 Gin Context。

### Q79：讲一下发布视频的全过程。

回答：

发布视频分两步。第一步是上传视频和封面，API 校验文件后临时落盘，调用 COS SDK 上传到对象存储，返回 play_url 和 cover_url。

第二步是调用 `/video/publish`，后端在 MySQL 事务里插入 videos 表，同时插入一条 outbox pending 消息，并解析标题和描述里的标签写入 tags 和 video_tags。

OutboxWorker 后台扫描 pending 消息，抢占为 processing，投递 timeline MQ。TimelineWorker 消费后把 videoID 写入 Redis 的 `feed:global_timeline` ZSet。这样新视频就能出现在最新 Feed 流里。

### Q80：讲一下刷最新 Feed 的全过程。

回答：

用户请求 `/feed/listLatest`，如果带 token 会走软鉴权得到 accountID。

后端先看 Redis 是否可用。Redis 可用时，读取 `feed:global_timeline` ZSet。如果 ZSet 为空，用 singleflight 查 MySQL 最新 1000 条重建。然后根据请求游标判断是热数据还是冷数据。

热数据从 Redis ZSet 按 score 倒序取 videoID，再走 GetVideosByIDs，通过本地缓存、Redis、MySQL 获取视频实体。如果热数据不足一页，再从 MySQL 拼接冷数据。最后批量查询当前用户是否点赞过这些视频，组装 FeedVideoItem 返回。

### Q81：讲一下热门榜全过程。

回答：

点赞、评论等行为会产生热度变化事件，PopularityWorker 消费后更新 Redis 的分钟级热度 ZSet。

用户请求热门榜时，后端取过去 60 分钟的分钟级 ZSet，使用 ZUNIONSTORE 合并为一个小时级临时榜，设置 2 分钟 TTL，然后按 offset/limit 取 videoID，再查视频实体并保持榜单顺序返回。

Redis 不可用时，降级 MySQL，按 popularity、create_time、id 排序和游标分页。

### Q82：讲一下点赞全过程。

回答：

用户调用点赞接口，API 鉴权拿到 accountID。RabbitMQ 可用时，LikeService 发布 like.like 事件到 like.events exchange。

like.events queue 的 LikeWorker 消费后，检查视频是否存在，插入 likes 表。如果 duplicate，说明已经点赞过，直接返回。首次点赞成功后，likes_count +1，popularity +1。

notification.like queue 也会收到 like.like 事件，NotificationWorker 消费后给视频作者生成通知。

### Q83：讲一下评论全过程。

回答：

用户调用评论发布接口，API 鉴权后构造 CommentEvent，带 event_id、video_id、author_id、content 等信息，发布到 comment.events exchange。

CommentWorker 消费 comment.events queue，检查视频存在后插入 comments 表。comments.event_id 是唯一索引，如果重复消费会 duplicate 并忽略。首次评论创建成功后，视频 popularity +1。

NotificationWorker 也会消费 comment.publish 事件，为视频作者生成评论通知。

### Q84：讲一下关注全过程。

回答：

用户调用关注接口，SocialService 发布 social.follow 事件。SocialWorker 消费后往 socials 表插入 follower_id 和 vlogger_id。这个组合有唯一索引，重复关注会 duplicate 并忽略。

NotificationWorker 同时消费 social.follow 事件，给被关注者生成通知。

### Q85：讲一下消息通知全过程。

回答：

点赞、评论、关注事件会被 notification 队列订阅。NotificationWorker 消费事件后，根据 routing key 判断通知类型，构造 Notification，写入 notifications 表。event_id 唯一索引用于通知幂等。

写库成功后，NotificationWorker 把 PushMessage 发布到 Redis Pub/Sub 的 `notification:push` 频道。所有 API 节点都有 NotificationSubscriber 订阅这个频道，收到消息后调用本节点 SSEHub.Push。只有持有目标用户 SSE 连接的节点会真正推送。

用户离线时，实时 SSE 收不到，但 notifications 表已经保存了通知，可以通过通知列表接口查询。

## 15. 分布式扩展

### Q86：你做了哪些分布式扩展？

回答：

第一，API 和 worker 进程拆分，可以独立扩容。

第二，Redis、MySQL、RabbitMQ、COS 作为共享状态，本地只保存临时状态，支持多 API 节点。

第三，文件上传迁移到 COS，避免多节点本地文件不共享。

第四，SSE 推送通过 Redis Pub/Sub 跨节点广播，解决用户连接节点和消息消费节点不一致。

第五，OutboxWorker 通过状态条件更新支持多 worker 节点抢占处理。

第六，RabbitMQ 同一队列多消费者天然竞争消费，可以横向扩展 worker。

### Q87：多 worker 消费同一个队列会不会重复消费同一条消息？

回答：

RabbitMQ 同一个队列下的多个消费者是竞争消费，一条消息正常只会投递给其中一个消费者。但由于消费失败、连接断开、ack 丢失等情况，消息仍可能重新投递，所以业务层仍然要做幂等。

### Q88：分布式下还有哪些不足？

回答：

MySQL 高可用还没有做，API 启动时 AutoMigrate 在生产多副本下也不合适，应该拆成独立迁移任务。

RabbitMQ 和 Redis 也需要高可用部署、监控和报警。死信队列目前能保存失败消息，但还缺自动补偿或人工重试后台。

日志和监控还可以继续完善，比如接口 P99、MQ 积压、DLX 数量、Redis 命中率、MySQL 慢查询、COS 上传耗时。

## 16. 项目难点与取舍

### Q89：这个项目最大的难点是什么？

回答：

我觉得最大难点是把短视频 Feed 里不同的异步链路和缓存链路设计清楚。

比如视频发布不是简单插入 videos 表，还要可靠更新 Redis timeline，所以引入 Outbox。点赞评论不是简单写表，还会影响计数、热度和通知，所以用 RabbitMQ 解耦，并在 worker 端做幂等。通知推送在单节点很简单，但多节点下用户连接在哪个 API 不确定，所以引入 Redis Pub/Sub。

这些点都涉及“业务正确性、性能和分布式部署”的平衡。

### Q90：你觉得项目最有技术含量的点是什么？

回答：

我认为是 Outbox + MQ + 幂等消费这一套。因为它不是简单使用 RabbitMQ，而是考虑了本地事务和消息投递的一致性、worker 多节点抢占、投递失败重试、重复投递可接受性和下游幂等。

另一个亮点是 Feed 的冷热数据和多级缓存设计，能解释清楚为什么 Redis 只存热数据、怎么防缓存击穿、怎么稳定分页。

### Q91：如果让你继续优化，你会优先做什么？

回答：

我会优先做三件事。

第一，配置 MySQL 连接池并做慢查询分析，因为压测时已经暴露了连接资源问题。

第二，补充监控和报警，尤其是 MQ 队列积压、死信队列、Redis 命中率、接口 P95/P99。

第三，完善生产化部署，比如数据库迁移独立化、健康检查、结构化日志、outbox failed 补偿工具。

## 17. 简历追问快答

### Q92：一句话讲多级缓存。

回答：

Feed 视频实体走本地缓存、Redis、MySQL 三级缓存；视频详情用 Redis 缓存和分布式锁防击穿；Redis 不可用时降级 MySQL。

### Q93：一句话讲 Outbox。

回答：

视频发布时在同一个 MySQL 事务中写 videos 和 outbox pending 消息，再由 OutboxWorker 后台投递 MQ，解决本地事务和消息投递不一致问题。

### Q94：一句话讲 MQ 幂等。

回答：

我不依赖 MQ exactly-once，而是在消费端用唯一索引、event_id 和 Redis ZSet member 唯一性处理重复消费。

### Q95：一句话讲 SSE 分布式推送。

回答：

NotificationWorker 写通知表后发布 Redis Pub/Sub，所有 API 节点订阅并调用本地 SSEHub，只有持有目标用户连接的节点真正推送。

### Q96：一句话讲热门榜。

回答：

热门榜用 Redis 分钟级 ZSet 记录热度变化，查询时合并最近 60 分钟窗口形成临时榜，Redis 不可用时降级 MySQL popularity 游标查询。

### Q97：一句话讲项目不足。

回答：

当前项目已经覆盖缓存、MQ、Outbox、幂等和分布式推送，但 MySQL 高可用、自动补偿、监控报警和更严格的热度幂等还可以继续生产化完善。
