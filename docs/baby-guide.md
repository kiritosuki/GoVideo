# GoVideo 项目保姆级复习手册

这份文档用于面试前复习 GoVideo 项目。它不是普通 README，而是按照面试官可能追问的路径来组织：项目整体怎么设计、核心流程怎么走、为什么这么做、失败场景怎么处理、有哪些取舍。

建议复习顺序：

```text
1. 先背熟项目一句话介绍和整体架构
2. 再重点掌握登录鉴权、多级缓存、Feed 流、MQ/worker、Outbox、幂等、通知推送
3. 最后准备好分布式部署、降级策略、测试和性能压测相关回答
```

## 1. 项目一句话介绍

GoVideo 是一个基于 Gin + Gorm 的短视频 Feed 流后端系统，支持用户登录注册、视频发布、Feed 流、点赞评论、关注关系、消息通知、对象存储上传等功能。

项目重点不是简单 CRUD，而是围绕后端常见的生产化问题做设计：

```text
高频读：Redis + 本地缓存 + MySQL 多级缓存
高频写：RabbitMQ 异步化 + worker 消费
可靠投递：Outbox 模式 + 状态机 + 重试
重复消息：事件 ID + 唯一索引 + 幂等消费
分布式推送：SSE + Redis Pub/Sub
对象存储：腾讯云 COS 存视频/封面/头像
工程化测试：单元测试 + 集成测试 + k6 性能测试
```

面试时可以这样介绍：

> 我做的是一个短视频 Feed 流后端系统，整体采用 Gin + Gorm + MySQL + Redis + RabbitMQ。MySQL 作为主数据源，Redis 用于缓存、限流、分布式锁、热门榜和通知 Pub/Sub，RabbitMQ 用于点赞、评论、关注、热度更新、时间线更新等异步任务。视频发布后使用 Outbox 模式保证本地事务和 MQ 投递之间的可靠性，通知推送使用 SSE + Redis Pub/Sub 解决多 API 节点下用户连接节点和消息消费节点不一致的问题。

## 2. 整体架构

当前推荐拆成两类进程：

```text
API 进程：
  处理 HTTP 请求
  JWT 鉴权
  SSE 长连接
  文件临时落盘并上传 COS
  投递 RabbitMQ 消息
  RabbitMQ 不可用时执行部分同步降级

Worker 进程：
  消费点赞、评论、关注消息
  消费热度更新消息
  扫描 outbox 表并投递 timeline 消息
  消费 timeline 消息并更新 Redis ZSet
```

推荐部署模型：

```text
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

共享状态：

```text
MySQL：账号、视频、点赞、评论、关注、通知、outbox
Redis：缓存、限流、分布式锁、Feed 派生数据、热度榜、Pub/Sub
RabbitMQ：异步事件队列
COS：视频、封面、头像对象存储
```

本地状态：

```text
SSEHub 的内存连接表
上传时的临时文件
FeedService 的本地短 TTL 缓存
```

面试追问：为什么拆 API 和 worker？

回答：

> API 进程主要关心请求响应延迟，worker 进程主要关心后台消费能力。拆开后可以分别扩容，比如读请求压力大就扩 API，消息积压就扩 worker。同时 worker 崩溃不会直接影响 API 接口进程，部署上也更符合生产环境的职责划分。

## 3. 项目分层结构

核心目录：

```text
cmd/main.go              API 入口
cmd/worker/main.go       worker 入口
internal/http/router.go  路由组装
internal/api/*           各业务模块 handler/service/repo/entity
internal/worker/*        后台消费者和 outbox/timeline worker
internal/middleware/*    Redis/RabbitMQ/JWT/COS/限流等基础设施封装
internal/db              数据库连接和 AutoMigrate
tests/integration        集成测试
tests/performance        k6 性能测试
docs                     文档
```

业务模块大体是：

```text
handler：解析 HTTP 请求、鉴权上下文、返回 JSON
service：业务逻辑、缓存、MQ 降级、事务组织
repo：数据库访问
entity：数据库模型和请求响应结构
```

面试追问：为什么 router 里还有对象组装？

回答：

> 当前 router 主要负责 HTTP 路由、handler/service/repo 组装和中间件绑定。MQ 底层声明封装在 rabbitmq 包中，worker 消费逻辑放在 worker 进程中。Notification 比较特殊，因为 SSEHub 属于 API 本地连接状态，所以通知订阅和本地推送入口仍然和 API 进程有关。

## 4. 登录、JWT 与单点登录逻辑

### 4.1 注册流程

注册接口：

```text
POST /account/register
```

核心逻辑：

```text
1. handler 解析 username/password
2. service 使用 bcrypt 给密码加密
3. repo 插入 accounts 表
```

关键代码：

```go
passwordHash, err := bcrypt.GenerateFromPassword([]byte(account.Password), bcrypt.DefaultCost)
account.Password = string(passwordHash)
return s.accountRepo.CreateAccount(ctx, account)
```

面试追问：为什么不能明文存密码？

回答：

> 明文密码一旦数据库泄露风险极高。这里使用 bcrypt 存哈希，bcrypt 自带 salt，并且计算成本可调，可以降低撞库和暴力破解风险。

### 4.2 登录流程

登录接口：

```text
POST /account/login
```

流程：

```text
1. 根据 username 查 MySQL
2. bcrypt 校验密码
3. 生成 access token
4. 生成 refresh token
5. 把 token 和 refresh token 写入 MySQL
6. Redis 可用时缓存 token 和 refresh token 映射
```

缓存结构：

```text
v1:account:{accountID}           -> access token
v1:account:{accountID}:refresh   -> refresh token
v1:refresh:{refreshToken}        -> accountID
```

关键代码：

```go
token, err := auth.GenerateToken(account.ID, account.Username)
refreshToken, err := auth.GenerateRefreshToken(account.ID)

err = s.accountRepo.UpdateTokenAndRefreshToken(ctx, account.ID, token, refreshToken)

s.cache.SetBytes(ctx, s.cache.Key("account:%d", account.ID), []byte(token), 24*time.Hour)
s.cache.SetBytes(ctx, s.cache.Key("account:%d:refresh", account.ID), []byte(refreshToken), 7*24*time.Hour)
s.cache.SetBytes(ctx, s.cache.Key("refresh:%s", refreshToken), []byte(strconv.FormatUint(uint64(account.ID), 10)), 7*24*time.Hour)
```

### 4.3 鉴权流程

普通受保护接口使用 `JWTAuth`：

```text
1. 从 Authorization 取 Bearer token
2. 解析 JWT，校验签名和过期时间
3. 先查 Redis 中 account:{id} 保存的 token
4. Redis 命中且 token 一致，放行
5. Redis 不可用或未命中，查 MySQL 兜底
6. 鉴权成功后把 accountID 和 username 写入 Gin Context
```

关键代码：

```go
bytes, err := cache.GetBytes(ctx, cache.Key("account:%d", claims.AccountID))
if err == nil {
    if string(bytes) != tokenStr {
        c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token has been revoked"})
        return
    }
    c.Set("accountID", claims.AccountID)
    c.Set("username", claims.Username)
    c.Next()
    return
}

accountInfo, err := accountRepo.FindByID(c.Request.Context(), claims.AccountID)
if err != nil || accountInfo.Token == "" || accountInfo.Token != tokenStr {
    c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token has been revoked"})
    return
}
```

### 4.4 为什么说当前是单点登录

因为每个账号在 MySQL 里只保存一份当前有效 token：

```text
用户 A 第一次登录 -> DB token = token1
用户 A 第二次登录 -> DB token = token2
旧 token1 再请求 -> JWT 本身可能没过期，但和 DB/Redis 中 token2 不一致，被拒绝
```

Redis 也是同样逻辑：

```text
account:{id} -> 当前最新 token
```

所以它不是传统意义上允许多端同时登录的 token 列表，而是“后登录覆盖先登录”。

面试追问：JWT 不是无状态的吗，为什么还查 Redis/MySQL？

回答：

> 纯 JWT 只要签名和过期时间合法就能用，服务端无法主动让某个 token 失效。这个项目为了支持登出、改名重新签发 token、单点登录等能力，把当前有效 token 存在 MySQL/Redis。这样 JWT 解析只是第一步，最终还要和服务端保存的 token 对比。

### 4.5 SoftJWTAuth 是什么

Feed 流这类接口允许匿名访问，但如果用户登录了，需要返回 `is_liked`。

所以设计了软鉴权：

```text
没有 token：直接放行，accountID = 0
有 token 且合法：写入 accountID，返回个性化字段
有 token 但非法：返回 401
```

用在：

```text
/feed/listLatest
/feed/listByPopularity
/feed/listByTag
```

## 5. Redis 设计

Redis 在项目里有多种角色：

```text
缓存：视频详情、Feed 视频实体、关注 Feed 响应、token
分布式锁：缓存击穿保护
限流：滑动窗口
Feed 派生数据：global timeline ZSet
热门榜：分钟级热度 ZSet
Pub/Sub：通知分布式广播
```

### 5.1 Key 统一前缀

Redis Client 的 Key 方法：

```go
func (c *Client) Key(format string, args ...any) string {
    prefix := ""
    if c != nil {
        prefix = c.keyPrefix
    }
    return prefix + fmt.Sprintf(format, args...)
}
```

默认前缀：

```text
v1:
```

作用：

```text
1. 避免不同环境或不同版本 key 冲突
2. 后续升级缓存结构时可以通过 v2: 做平滑切换
3. c == nil 时也能安全生成 key，避免空指针
```

### 5.2 分布式锁

使用 Redis `SET NX PX`：

```go
ok, err := c.rdb.SetNX(ctx, key, token, ttl).Result()
```

释放锁用 Lua，保证“只能删除自己的锁”：

```lua
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
```

为什么要 token？

```text
线程 A 加锁成功，锁 TTL 过期
线程 B 加锁成功
线程 A 业务终于执行完，如果直接 DEL，会误删 B 的锁
所以释放锁前必须判断 value 是否是自己的 token
```

项目中用于缓存击穿保护：

```text
视频详情缓存 miss
只有一个请求抢到 lock:video:detail:id={id}
抢到锁的请求查 MySQL 并回写缓存
其他请求短暂等待缓存回填
等待失败再降级查 MySQL
```

## 6. 滑动窗口限流

项目最开始可以理解成固定窗口限流，后来改成了滑动窗口。

限流入口：

```go
Limit(cache, "account_login", 10, time.Minute, KeyByIP)
Limit(cache, "comment_write", 10, time.Minute, KeyByAccount)
```

当前路由中的限流策略：

```text
登录：IP 维度，每分钟 10 次
注册：IP 维度，每小时 5 次
点赞：账号维度，每分钟 30 次
评论：账号维度，每分钟 10 次
关注：账号维度，每分钟 20 次
```

滑动窗口 Lua：

```lua
redis.call("ZREMRANGEBYSCORE", key, 0, minScore)
local count = redis.call("ZCARD", key)
if count >= limit then
    redis.call("PEXPIRE", key, ttl)
    redis.call("PEXPIRE", seqKey, ttl)
    return {0, count}
end

local seq = redis.call("INCR", seqKey)
local member = tostring(now) .. "-" .. tostring(seq)
redis.call("ZADD", key, now, member)
return {1, count + 1}
```

核心思路：

```text
ZSet score = 请求时间戳毫秒
每次请求先删除窗口外的旧请求
ZCARD 得到当前窗口内请求数
如果超过上限拒绝
否则把当前请求写入 ZSet
```

为什么有 `key + ":seq"`？

```text
同一毫秒内可能有多个请求
ZSet member 不能重复
所以用 now-seq 作为唯一 member
score 仍然是 now
```

为什么用 Lua？

> 删除旧请求、统计数量、判断是否允许、写入新请求必须是一个原子操作。如果拆成多条 Redis 命令，高并发下多个请求可能同时看到 count 未超限，然后都放行，导致限流失效。

Redis 不可用怎么办？

```go
if err != nil {
    c.Next()
    return
}
```

也就是限流失败时默认放行，避免 Redis 故障导致整个服务不可用。这是可用性优先的设计。

## 7. 多级缓存体系

简历上写了 Redis + 本地缓存 + MySQL，多级缓存主要体现在 FeedService 的 `GetVideosByIDs` 和 VideoService 的 `GetDetail`。

### 7.1 Feed 视频实体三级缓存

`GetVideosByIDs` 用于根据一组视频 ID 批量获取视频实体。

缓存层级：

```text
L1 本地缓存：go-cache，TTL 约 5s
L2 Redis：video:entity:{id}
L3 MySQL：videos 表
```

流程：

```text
1. 遍历 videoIDs，先查本地缓存
2. 本地未命中的 ID，批量 MGet Redis
3. Redis 命中的视频反序列化后回写本地缓存
4. Redis 未命中的视频查 MySQL
5. MySQL 查询结果异步回写 Redis，并写入本地缓存
6. 最后按原始 videoIDs 顺序返回
```

为什么要保持原顺序？

> Feed 列表里的视频 ID 顺序可能来自 Redis ZSet 或热门榜，代表业务排序。MySQL `WHERE id IN (...)` 不保证返回顺序，所以必须用 map 重新按 ID 顺序组装。

关键代码：

```go
results, err := s.redisCache.MGet(cacheCtx, cacheKeys...)
for i, res := range results {
    id := missedL1[i]
    if res != nil {
        var v video.Video
        if err := json.Unmarshal([]byte(str), &v); err == nil {
            videoMap[id] = &v
            s.localCache.Set(cacheKeys[i], v, 5*time.Second)
            continue
        }
    }
    missedL2 = append(missedL2, id)
}
```

MySQL 查询使用 singleflight 防止击穿：

```go
sfKey := s.redisCache.Key("sf:entity:%d", videoID)
v, err, _ := s.requestGroup.Do(sfKey, func() (interface{}, error) {
    return s.feedRepo.GetByID(ctx, videoID)
})
```

面试追问：为什么本地缓存 TTL 只有几秒？

回答：

> 本地缓存是每个 API 进程自己的内存，不能跨节点失效通知。如果 TTL 太长，数据一致性会变差。这里用几秒短 TTL，主要抵挡瞬时热点请求，降低 Redis 和 MySQL 压力，同时把不一致窗口控制得很短。

### 7.2 视频详情缓存击穿保护

`/video/getDetail` 主要逻辑在 `VideoService.GetDetail`。

流程：

```text
1. Redis 可用时先查 video:detail:id={id}
2. 命中直接返回
3. Redis miss 时尝试抢分布式锁
4. 抢到锁：双查缓存 -> 查 MySQL -> 回写 Redis -> 返回
5. 没抢到锁：每 20ms 查一次缓存，最多 5 次
6. 等不到缓存则降级查 MySQL
7. Redis 故障时直接查 MySQL
```

关键代码：

```go
token, ok, err := s.cache.Lock(lockCtx, lockKey, 2*time.Second)
if err == nil && ok {
    defer s.cache.Unlock(context.Background(), lockKey, token)
    if cached, ok := getCacheFunc(); ok {
        return cached, nil
    }
    video, err := s.videoRepo.FindByID(ctx, id)
    setCacheFunc(video)
    return video, nil
}
```

为什么抢到锁后还要再次查缓存？

> 因为从第一次缓存 miss 到抢到锁之间存在时间窗口，可能其他请求已经查库并回写缓存了。再次查缓存可以避免重复查 MySQL。

### 7.3 Redis 不可用时的降级

项目整体设计是 Redis 尽量可选：

```text
token 缓存：降级查 MySQL
视频详情：降级查 MySQL
Feed 视频实体：降级本地缓存 + MySQL
限流：Redis 故障时放行
分布式锁：Redis 故障时跳过锁，业务继续
通知实时推送：Redis 故障时只能本节点本地推送，但通知已入库
```

面试追问：Redis 不可用会不会影响正确性？

回答：

> 大部分场景 Redis 是性能优化，不是最终数据源。MySQL 仍然是主数据源，所以视频详情、Feed、登录鉴权等可以降级。比较特殊的是分布式 SSE 实时推送，它在多 API 节点下依赖 Redis Pub/Sub 做跨节点广播；Redis 不可用时实时推送可能不可靠，但通知已经写入 MySQL，用户仍可通过通知列表查询。

## 8. Feed 流设计

项目中的 Feed 不是推荐算法系统，而是围绕短视频常见查询设计了几类列表：

```text
listLatest：全局最新视频流
listByPopularity：热门榜
listByFollowing：关注的人发布的视频
listByTag：按标签查询
listLikesCount：按点赞数排序
```

### 8.1 全局最新 Feed：冷热数据设计

全局最新流使用 Redis ZSet 存热数据：

```text
key: feed:global_timeline
member: videoID
score: create_time 毫秒时间戳
```

`TimelineWorker` 消费 timeline 消息后写入：

```go
w.cache.ZAdd(ctx, "feed:global_timeline", redis.Z{
    Score:  float64(evt.CreateTime),
    Member: fmt.Sprintf("%d", evt.VideoID),
})
w.cache.ZRemRangeByRank(ctx, timelineKey, 0, -1001)
```

只保留最新 1000 条：

```text
Redis 只保存热数据
更早的数据作为冷数据，从 MySQL 查
```

ListLatest 流程：

```text
1. Redis 不可用：直接查 MySQL
2. Redis ZSet 为空：singleflight 触发一次 MySQL 最新 1000 条重建
3. 读取 ZSet 最旧一条 score 作为 watermark
4. 如果请求游标早于 watermark：说明查的是冷数据，直接查 MySQL
5. 如果请求游标在热区：从 Redis ZSet 按时间倒序取 videoIDs
6. 根据 videoIDs 走 GetVideosByIDs 多级缓存拿视频实体
7. 如果热数据不足一页，再从 MySQL 拼接冷数据
```

核心判断：

```text
watermark = Redis 中最旧热数据的 create_time
reqTime <= watermark -> 冷数据
reqTime > watermark  -> 热数据
```

面试追问：为什么不把所有视频都放 Redis？

回答：

> 短视频列表访问通常有明显时间热点，用户更常刷最近发布的视频。把所有视频都放 Redis 会浪费内存，而且维护成本高。这里 Redis 只维护最新 1000 条作为热区，超过热区的冷数据从 MySQL 分页查。这样兼顾性能和内存成本。

面试追问：ZSet 空了怎么办？

回答：

> 可能是 Redis 重启或缓存被清空。此时用 singleflight 保证只有一个请求查 MySQL 最新 1000 条重建 ZSet，其他并发请求等待同一个结果，然后递归重新走正常查询流程，避免缓存重建时大量请求同时打 MySQL。

核心代码：

```go
v, err, _ := s.requestGroup.Do(sfKey, func() (interface{}, error) {
    videos, err := s.feedRepo.ListLatest(ctx, 1000, time.Time{})
    for _, vi := range videos {
        zElements = append(zElements, redis.Z{
            Score:  float64(vi.CreateTime.UnixMilli()),
            Member: fmt.Sprintf("%d", vi.ID),
        })
    }
    s.redisCache.ZAdd(rebuildCtx, "feed:global_timeline", zElements...)
    return "SUCCESS", nil
})
return s.ListLatest(ctx, limit, latestBefore, accountID)
```

面试追问：冷热边界怎么处理？

回答：

> 如果 Redis 热区查出来不足一页，说明请求刚好落在热区和冷区边界。这时以当前页最后一个视频的创建时间作为 coldCursor，再去 MySQL 查剩余数量的数据进行拼接。这样不会因为 Redis 只保留 1000 条就导致分页突然断掉。

### 8.2 热门榜设计

热门榜使用分钟级 ZSet：

```text
hot:video:1m:{yyyyMMddHHmm}
member: videoID
score: popularity change
```

`PopularityWorker` 消费点赞/评论产生的热度变化，更新 Redis 热度缓存。

ListByPopularity 查询时：

```text
1. 以 asOf 为当前分钟
2. 找过去 60 个分钟级热度 ZSet
3. ZUNIONSTORE 合并为小时级临时榜
4. 临时榜 TTL 2 分钟
5. 按 offset/limit 从合并榜查询 videoIDs
6. 查 MySQL 获取视频实体并按榜单顺序返回
7. Redis 不可用时降级 MySQL 按 popularity/create_time/id 游标查
```

核心代码：

```go
for i := 0; i < 60; i++ {
    keys = append(keys, s.redisCache.Key("hot:video:1m:%s", asOf.Add(-time.Duration(i)*time.Minute).Format("200601021504")))
}
dest := s.redisCache.Key("hot:video:merge:1m:%s", asOf.Format("200601021504"))
s.redisCache.ZUnionStore(ctx, dest, keys, "SUM")
s.redisCache.Expire(ctx, dest, 2*time.Minute)
```

为什么用分钟窗口？

> 热榜不是简单全量排序，而是希望反映近期热度。分钟窗口可以把点赞、评论等行为按时间切片记录，查询时合并最近 60 分钟，既能体现近期热度，又避免每次实时扫描全量视频。

为什么合并榜只缓存 2 分钟？

> 合并榜是派生数据，生成成本不算特别高，但每次都合并 60 个 ZSet 也有开销。缓存 2 分钟可以复用短时间内的热门榜结果，同时保证榜单不会太旧。

Redis 挂了怎么办？

```text
降级 MySQL：
ORDER BY popularity DESC, create_time DESC, id DESC
使用 popularity + create_time + id 作为游标
```

为什么游标要三个字段？

> 因为 popularity 可能相同，create_time 也可能相同。使用 popularity、create_time、id 组成复合游标，可以保证分页顺序稳定，避免重复或漏数据。

### 8.3 关注 Feed

关注 Feed 查询：

```text
查 social 表中 follower_id = 当前用户 的 vlogger_id
再查 videos.author_id in (这些 vlogger_id)
按 create_time desc 分页
```

SQL 思路：

```go
followingQuery := db.Model(&social.Social{}).
    Select("vlogger_id").
    Where("follower_id = ?", accountID)

query = query.Where("author_id in (?)", followingQuery)
```

关注 Feed 也有 Redis 缓存：

```text
feed:listByFollowing:limit={limit}:accountID={id}:before={cursor}
```

缓存 miss 时使用分布式锁防击穿。

## 9. RabbitMQ 消息队列设计

项目中 RabbitMQ 用于异步化：

```text
点赞/取消点赞
评论发布/删除
关注/取消关注
热度更新
通知生成
时间线更新
```

### 9.1 RabbitMQ 底层封装

连接：

```go
conn, err := amqp.Dial(url)
ch, err := conn.Channel()
```

`RabbitMQ` 保存：

```go
type RabbitMQ struct {
    Conn *amqp.Connection
    Ch   *amqp.Channel
}
```

注意：

```text
Connection 是 TCP 连接
Channel 是 RabbitMQ 在连接上的轻量级逻辑通道
Queue/Exchange 是 broker 里的资源，不等于 Channel
多个 Channel 连接到同一个 broker/vhost，声明同名 exchange/queue 是同一份资源
```

为什么每个 worker 单独 channel？

> RabbitMQ 官方建议不要在多个 goroutine 中并发复用同一个 channel。每个 worker 有自己的 channel，可以避免并发消费、ack、publish 之间相互干扰。如果某个 channel 异常，也不一定直接影响其他 channel。

项目里通过：

```go
func (r *RabbitMQ) NewChannelClient() (*RabbitMQ, error) {
    ch, err := r.Conn.Channel()
    return &RabbitMQ{Conn: r.Conn, Ch: ch}, nil
}
```

### 9.2 Topic Exchange 和 Queue

统一声明方法：

```go
ExchangeDeclare(exchange, "topic", durable=true)
QueueDeclare(queue, durable=true, args={"x-dead-letter-exchange": DLXExchange})
QueueBind(queue, bindingKey, exchange)
```

典型拓扑：

```text
like.events exchange
  like.events queue               binding like.*
  notification.like queue         binding like.like

comment.events exchange
  comment.events queue            binding comment.*
  notification.comment queue      binding comment.publish

social.events exchange
  social.events queue             binding social.*
  notification.social queue       binding social.follow
```

这样做的好处：

```text
业务 worker 消费主业务队列
notification worker 只关心需要生成通知的事件
同一条 like.like 消息可以被 like worker 和 notification worker 各自消费一份
```

面试追问：RabbitMQ 一个消息会被多个队列消费吗？

回答：

> 消息发布到 exchange，exchange 根据 routing key 和 binding key 路由到一个或多个队列。每个队列会收到一份消息。同一个队列内部的多个消费者是竞争消费，但不同队列之间是广播式各拿一份。

### 9.3 消费重试与死信队列

所有 worker 复用 `consumeWithRetry`：

```text
autoAck=false
process 成功 -> Ack
process 失败 -> 读取 x-retry-count
未超过 3 次 -> 手动 republish 到原 exchange/routingKey，retry+1，然后 Ack 原消息
超过 3 次 -> Nack(requeue=false)，进入死信队列
```

关键代码：

```go
if err := process(ctx, d); err != nil {
    retryCount := getRetryCount(d)
    if retryCount >= rabbitmq.MaxRetryCount {
        _ = d.Nack(false, false)
        return
    }
    republishForRetry(ctx, ch, d, retryCount+1)
    _ = d.Ack(false)
    return
}
_ = d.Ack(false)
```

为什么不用直接 Nack requeue=true？

> 直接 requeue 无法可靠记录重试次数，可能无限重试。这里手动 republish 时在 header 里写 `x-retry-count`，可以控制最多重试次数，超过后进入死信队列，方便后续排查和人工补偿。

死信队列：

```text
普通 queue 声明 x-dead-letter-exchange = dlx.events
每个 queue 对应一个 {queue}.dlx
```

例如：

```text
like.events.dlx
comment.events.dlx
social.events.dlx
video.timeline.events.dlx
```

面试追问：重试一共几次？

回答：

> 首次消费不算 retry，失败后最多 republish 3 次，所以总共最多处理 4 次：首次 + 3 次重试。超过后进入死信队列。

### 9.4 worker 进程退出策略

worker 的 `Run` 返回一般是不可恢复的进程级错误，例如：

```text
channel consume 失败
deliveries channel closed
ctx canceled
```

当前策略：

```text
非 context.Canceled 错误 -> 进程退出
依赖 Docker/Kubernetes 自动重启恢复
```

为什么不在代码里无限重连？

> 进程级错误可能是 RabbitMQ 连接断开、channel 状态异常或配置错误。自己写无限重连容易隐藏问题，也可能在不可恢复错误下不断空转。生产环境更常见的是进程失败后由容器编排系统重启，并配合健康检查和日志监控。

## 10. 点赞、评论、关注异步流程

### 10.1 点赞流程

接口：

```text
POST /like/like
```

RabbitMQ 可用：

```text
API -> LikeMQ.Publish(like.like) -> like.events exchange
like.events queue -> LikeWorker 消费
notification.like queue -> NotificationWorker 消费
```

LikeWorker：

```text
1. 校验 video 是否存在
2. 插入 likes 表
3. 如果 duplicate，说明重复点赞，直接返回 nil
4. 新点赞成功才 likes_count +1
5. 新点赞成功才 popularity +1
```

幂等关键：

```go
created, err := w.likeRepo.LikeIgnoreDuplicate(ctx, &like.Like{
    VideoID: videoID,
    AccountID: userID,
})
if !created {
    return nil
}
w.videoRepo.ChangeLikesCount(ctx, videoID, 1)
```

为什么点赞天然适合唯一索引幂等？

> 一个用户对一个视频最多点赞一次，所以 `(video_id, account_id)` 可以建唯一索引。重复消息再次插入会触发 duplicate，忽略即可，不会重复增加点赞数。

取消点赞：

```text
Delete where video_id=? and account_id=?
RowsAffected > 0 才 likes_count -1
重复删除 RowsAffected=0，不影响业务
```

### 10.2 评论流程

评论和点赞不同：同一个用户可以对同一个视频发表多条评论，不能用 `(video_id, author_id)` 做唯一约束。

所以评论使用事件 ID 幂等：

```text
CommentEvent.EventID
comments.event_id unique index
```

流程：

```text
API 生成 CommentEvent，带 event_id
CommentWorker 消费
CreateCommentIgnoreDuplicate
重复 event_id -> duplicate -> 返回 nil
首次创建成功 -> 视频 popularity +1
```

关键代码：

```go
c := &comment.Comment{
    EventID:  evt.EventID,
    Username: evt.Username,
    VideoID:  evt.VideoID,
    AuthorID: evt.AuthorID,
    Content:  evt.Content,
}
created, err := w.commentRepo.CreateCommentIgnoreDuplicate(ctx, c)
if !created {
    return nil
}
return w.videoRepo.ChangePopularity(ctx, evt.VideoID, 1)
```

面试追问：为什么评论删除没有减少热度？

回答：

> 当前热度模型中评论发布会增加热度，删除评论不回滚热度。这是业务取舍，类似很多热度系统会把互动行为作为历史信号，而不是严格实时计数。如果要更严格，可以在删除评论时发送 popularity -1 事件。

### 10.3 关注流程

关注关系：

```go
uniqueIndex: idx_socials_follower_vlogger
```

也就是：

```text
(follower_id, vlogger_id) 唯一
```

关注重复消息：

```text
插入 duplicate -> 忽略
```

取消关注：

```text
硬删除
重复删除不破坏业务
```

## 11. Outbox 模式

这是项目最重要的亮点之一。

### 11.1 为什么需要 Outbox

视频发布需要做两件事：

```text
1. 写 videos 表
2. 发 MQ 消息通知 timeline worker 更新 Redis Feed 时间线
```

如果直接这样写：

```text
insert video 成功
publish MQ 失败
```

就会出现：

```text
MySQL 里有视频
Redis Feed 时间线里没有这个视频
```

反过来：

```text
publish MQ 成功
insert video 失败
```

也会出现消息引用了不存在的视频。

本地事务无法直接覆盖 RabbitMQ，所以用 Outbox。

### 11.2 视频发布事务

`VideoService.Publish` 中用 MySQL 事务：

```text
1. 插入 videos 表
2. 插入 outbox_msgs 表，状态 pending
3. 提取 #tag 并写 tags/video_tags
4. 事务提交
```

关键代码：

```go
err := s.videoRepo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
    if err := tx.Create(video).Error; err != nil {
        return err
    }
    msg := &OutboxMsg{
        VideoID: video.ID,
        EventType: "video_published",
        Status: OutboxStatusPending,
    }
    if err := tx.Create(msg).Error; err != nil {
        return err
    }
    return nil
})
```

这样保证：

```text
视频和 outbox 消息要么同时成功
要么同时失败
```

### 11.3 OutboxWorker 状态机

状态：

```text
pending：待投递
processing：某个 worker 正在处理
published：已成功投递 MQ
failed：超过重试次数，失败留库
```

处理流程：

```text
1. 每秒扫描 pending，默认最多 100 条
2. 对每条消息执行 claim
3. claim 成功：pending -> processing
4. 投递 timeline MQ
5. 投递成功：processing -> published
6. 投递失败：retry_count +1
7. retry_count <= 3：重新变 pending，等待下次扫描
8. retry_count > 3：变 failed，留库
```

核心抢占代码：

```go
res := db.Model(&video.OutboxMsg{}).
    Where("id = ? AND status = ?", id, OutboxStatusPending).
    Updates(map[string]any{
        "status": OutboxStatusProcessing,
        "updated_at": time.Now(),
    })
return res.RowsAffected == 1
```

为什么这样可以支持多节点？

> 多个 OutboxWorker 可能同时扫描到同一行 pending，但只有一个节点能成功执行 `where id=? and status=pending` 的更新。RowsAffected=1 的节点抢占成功，其他节点 RowsAffected=0，直接放弃。这相当于用数据库状态更新做乐观抢占。

### 11.4 processing 超时回收

问题：

```text
worker 抢占成功，状态改成 processing
还没来得及 publish 或 markPublished，进程崩溃
这条消息会一直卡在 processing
```

解决：

```go
deadline := time.Now().Add(-w.timeout)
db.Model(&video.OutboxMsg{}).
    Where("status = ? AND updated_at < ?", OutboxStatusProcessing, deadline).
    Updates(map[string]any{
        "status": OutboxStatusPending,
        "updated_at": time.Now(),
    })
```

默认超时：

```text
5 分钟
```

面试追问：会不会重复投递？

回答：

> 有可能。例如 worker publish 成功，但还没来得及把 outbox 标记为 published 就崩溃，超时后其他节点会重新投递。这是 Outbox 常见的 at-least-once 语义。当前下游 timeline 使用 Redis ZSet，member 是 videoID，重复写入会覆盖同一个 member，所以可以接受。

### 11.5 为什么 OutboxWorker 还要投 MQ，不直接更新 Redis？

这是之前你重点问过的问题。

可以这样回答：

> 如果 OutboxWorker 扫描 outbox 后直接更新 Redis，就把“扫描数据库”和“更新时间线派生数据”耦合在一个 worker 中。加 MQ 的好处是职责拆分：OutboxWorker 只负责可靠地把本地表事件投递出去，TimelineWorker 只负责消费事件并更新 Redis。后续如果 timeline 更新逻辑变复杂，或者需要多个消费者订阅视频发布事件，就不需要改 OutboxWorker。

但是也要诚实说明：

> 对于当前项目规模，直接扫描 outbox 并更新 Redis 也能工作。使用 MQ 是更偏生产化和可扩展的设计，牺牲了一点复杂度，换来职责解耦和扩展能力。

## 12. TimelineWorker 与 Redis ZSet 幂等

TimelineWorker 消费 `video.timeline.events` 队列。

事件：

```json
{
  "event_id": "...",
  "action": "publish",
  "video_id": 1,
  "create_time": 1710000000000
}
```

处理：

```go
ZADD feed:global_timeline score=create_time member=videoID
ZREMRANGEBYRANK feed:global_timeline 0 -1001
```

为什么 ZSet 天然幂等？

> ZSet 的 member 唯一，同一个 videoID 重复 ZADD 不会产生两条记录，只会更新 score。因此 outbox 重复投递或 MQ 重复消费不会让 Feed 里出现重复视频。

注意：

> 如果重复消息的 score 有微小差异，会更新该 videoID 的 score。但当前 timeline event 使用视频 create_time，重复投递时 create_time 来自 outbox，不应该变化。因此实际影响很小。

## 13. 幂等性总览

面试官很可能追问“消息重复消费怎么办”。

你可以直接按 worker 分类回答。

### 13.1 LikeWorker

```text
点赞：唯一索引 + duplicate 忽略
取消点赞：硬删除，RowsAffected=0 不再扣减
```

结论：

```text
已处理幂等
```

### 13.2 SocialWorker

```text
关注：唯一索引 + duplicate 忽略
取消关注：硬删除，可重复执行
```

结论：

```text
已处理幂等
```

### 13.3 CommentWorker

```text
评论发布：event_id 唯一索引
重复消息：Create duplicate -> 返回 nil -> Ack
只有首次创建成功才更新 popularity
```

结论：

```text
已处理幂等
```

### 13.4 NotificationWorker

```text
notifications.event_id unique
重复通知消息直接 duplicate 忽略
```

关键代码：

```go
if err := db.Create(notif).Error; err != nil {
    var mysqlErr *mysql.MySQLError
    if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
        return nil
    }
    return err
}
```

结论：

```text
已处理幂等
```

### 13.5 TimelineWorker

```text
Redis ZSet member = videoID
重复 ZADD 不会产生重复视频
```

结论：

```text
可接受的天然幂等
```

### 13.6 OutboxWorker

```text
保证 at-least-once，不保证 exactly-once
可能重复投递
下游 timeline 能容忍
```

结论：

```text
当前业务可接受，严格幂等可作为后续优化
```

### 13.7 PopularityWorker

```text
当前未实现严格幂等
重复消费可能导致热度轻微偏差
业务上暂时可容忍
```

面试时要主动说明：

> 热度榜本身是近似指标，不是强一致计数。重复消费概率低，即使发生也只是榜单分值轻微偏差，不影响主业务正确性。如果要继续优化，可以给 popularity event 维护去重表或 Redis 去重 key。

## 14. 通知系统：SSE + Redis Pub/Sub

这是另一个很容易被追问的亮点。

### 14.1 为什么不用 WebSocket

当前通知只需要服务端向客户端推送，客户端不需要通过同一连接频繁发消息。

SSE 特点：

```text
基于 HTTP
天然服务端 -> 客户端单向推送
浏览器 EventSource 支持好
实现比 WebSocket 简单
断线重连机制更简单
```

所以选择 SSE 推送通知。

### 14.2 SSEHub 本地连接表

SSEHub：

```go
type SSEHub struct {
    mu sync.RWMutex
    clients map[uint][]chan *Notification
    db *gorm.DB
}
```

为什么是 `map[userID][]channel`，不是一个 channel？

> 虽然当前 token 逻辑偏单点登录，但同一个用户仍可能打开多个浏览器标签页，或者前端重连瞬间存在多个 SSE 连接。用切片可以支持同一用户多个连接同时收到通知。

### 14.3 SSEHandler 为什么是 for 循环

SSE 本质仍然是一个 HTTP 长连接请求。Handler 接收到请求后不立即返回，而是在当前请求上下文里阻塞等待消息：

```go
for {
    select {
    case <-ctx.Done():
        return
    case n := <-ch:
        fmt.Fprintf(c.Writer, "data: %s\n\n", b)
        flusher.Flush()
    case <-time.After(30 * time.Second):
        fmt.Fprintf(c.Writer, ": keepalive\n\n")
        flusher.Flush()
    }
}
```

这不是 bug，而是 SSE 的正常工作方式。Gin/Go HTTP server 会为请求分配 goroutine，长连接就由这个请求 goroutine 持有。

### 14.4 单节点通知流程

```text
用户连接 /notification/stream
SSEHub.Subscribe(userID)
NotificationWorker 消费 like/comment/follow 消息
写 notifications 表
调用 hub.Push(userID, notification)
SSEHandler 从 channel 取出并写入 HTTP 响应
```

### 14.5 多节点为什么有问题

如果部署：

```text
api-1 持有用户 A 的 SSE 连接
api-2 的 NotificationWorker 消费到通知消息
```

api-2 调用自己的 hub.Push：

```text
api-2 本地没有用户 A 的连接
消息推不出去
```

因为 SSEHub 是每个 API 节点自己的内存状态，不能跨节点共享。

### 14.6 Redis Pub/Sub 解决方案

当前流程：

```text
NotificationWorker 消费 RabbitMQ
写 notifications 表
Publish PushMessage 到 Redis 频道 notification:push
每个 API 节点启动 NotificationSubscriber 订阅该频道
所有节点收到消息后都调用本节点 hub.Push
只有真正持有用户 SSE 连接的节点能推送成功
没有连接的节点 hub.Push 直接返回，不会积压
```

关键代码：

```go
msg := notification.PushMessage{
    RecipientID: notif.RecipientID,
    Notification: *notif,
}
w.cache.PublishJSON(ctx, notification.PushChannel, msg)
```

订阅：

```go
pubsub, err := cache.Subscribe(ctx, notification.PushChannel)
go func() {
    ch := pubsub.Channel()
    for msg := range ch {
        var pushMsg notification.PushMessage
        json.Unmarshal([]byte(msg.Payload), &pushMsg)
        hub.Push(pushMsg.RecipientID, &pushMsg.Notification)
    }
}()
```

面试追问：所有节点都 Push，会不会重复推送？

回答：

> 所有节点都会收到 Redis Pub/Sub 消息，但 `hub.Push` 只会推送给本节点内存里存在的连接。如果用户只连接在 api-1，那么 api-2/api-3 的 clients 里没有这个 userID，会直接返回，不会推送，也不会积压。只有持有目标连接的节点真正发送 SSE。

面试追问：用户离线了怎么办？

回答：

> Redis Pub/Sub 只负责在线实时推送，不保证离线消息。通知在推送前已经写入 MySQL notifications 表，所以用户离线后不会收到实时 SSE，但重新上线可以通过通知列表接口查询历史通知。

### 14.7 Redis 故障降级

如果 Redis Pub/Sub 发布失败：

```go
if err := w.cache.PublishJSON(...); err == nil {
    return nil
} else {
    w.hub.Push(notif.RecipientID, notif)
}
```

降级效果：

```text
单节点：仍然可以本地推送
多节点：只有消费消息的节点刚好持有用户连接时才推送成功
所有场景：通知已经写入 MySQL，不丢历史通知
```

面试时要明确：

> Redis 不可用时，分布式实时推送能力会降级，但通知数据不丢。在线实时性依赖 Redis Pub/Sub，离线可靠性依赖 MySQL。

## 15. 对象存储 COS

### 15.1 为什么用 COS

最初如果把视频、封面、头像存在 API 本地目录，多节点部署会有问题：

```text
用户上传到 api-1
请求详情打到 api-2
api-2 本地没有这个文件
```

所以迁移到对象存储：

```text
所有 API 节点上传到同一个 COS bucket
数据库只保存对象可访问 URL
任意节点返回的 URL 都可访问
```

### 15.2 当前上传流程

```text
1. 前端 multipart 上传文件到 API
2. API 校验大小和后缀
3. 保存到 .run/uploads/... 临时文件
4. 调用 COS SDK UploadFile
5. 上传成功后删除本地临时文件
6. 返回 COS URL
```

为什么先落盘？

> 当前使用 COS SDK 的高级上传接口 `Object.Upload`，它根据文件大小自动选择普通上传或分块上传，适合大视频。这个接口需要本地文件路径，所以先临时落盘。临时文件不是最终存储，上传成功后会删除。

关键代码：

```go
if _, _, err := c.client.Object.Upload(ctx, key, filePath, nil); err != nil {
    return "", err
}
return c.ObjectURL(key)
```

对象 key：

```text
videos/{authorID}/{date}/{random}.mp4
covers/{authorID}/{date}/{random}.jpg
avatars/{accountID}/{random}.jpg
```

### 15.3 Put 和 Upload 的区别

腾讯云 SDK：

```text
Put：普通上传，适合小文件和流式 body
Upload：高级上传，小文件普通上传，大文件自动分块上传
```

短视频可能达到 200MB，所以选择 Upload 更稳。

## 16. 数据库与索引设计

核心表：

```text
accounts
videos
likes
comments
socials
notifications
outbox_msgs
tags
video_tags
```

重要索引：

```text
videos.create_time                         最新 Feed
videos.likes_count, id                     点赞榜
videos.popularity, create_time, id         热门榜 MySQL 降级
likes.video_id, account_id                 点赞状态和幂等
comments.event_id                          评论幂等
socials.follower_id, vlogger_id            关注关系幂等
notifications.event_id                     通知幂等
outbox_msgs.status                         outbox 扫描
outbox_msgs.published_at                   投递状态查询
```

面试追问：为什么热门榜 MySQL 降级要建复合索引？

回答：

> 查询是 `ORDER BY popularity DESC, create_time DESC, id DESC`，分页游标也基于这三个字段。如果没有对应复合索引，MySQL 可能需要 filesort 或扫描大量数据。复合索引能让排序和游标查询更高效。

## 17. 中间件故障降级

### 17.1 Redis 故障

```text
视频详情：查 MySQL
Feed：查 MySQL 或本地缓存 + MySQL
JWT：查 MySQL token
限流：放行
分布式锁：跳过锁
通知：实时分布式推送降级，本地推送 + MySQL 历史通知
热门榜：降级 MySQL popularity 查询
```

### 17.2 RabbitMQ 故障

API 层部分业务有同步 fallback：

```text
点赞：同步写 MySQL / 更新缓存
取消点赞：同步删除
评论：同步写 MySQL
关注：同步写 MySQL
```

但以下能力会受影响：

```text
worker 消费不可用
通知异步生成受影响
timeline 派生数据更新受影响
热度异步更新受影响
```

### 17.3 MySQL 故障

MySQL 是主数据源，当前强依赖 MySQL。

回答要直接：

> Redis 和 RabbitMQ 的降级都建立在 MySQL 可用的前提上。MySQL 故障时主业务不可用，这需要数据库高可用、备份恢复、读写分离等生产化方案。

### 17.4 COS 故障

```text
上传接口失败
不会返回本地 URL
已上传成功的历史资源不受影响
```

## 18. 分布式部署下的关键问题

### 18.1 API 多节点

需要共享：

```text
MySQL
Redis
RabbitMQ
COS
```

不能依赖：

```text
本地文件作为永久存储
本地内存保存跨节点业务状态
```

项目中：

```text
文件最终存 COS
SSE 跨节点靠 Redis Pub/Sub
Feed 派生数据在 Redis
token 状态在 MySQL/Redis
```

### 18.2 worker 多节点

RabbitMQ 同一队列多消费者天然竞争消费：

```text
like.events queue
  worker-1
  worker-2
```

同一条消息只会被其中一个 worker 消费。

Outbox 扫描不是 RabbitMQ 队列，需要自己处理抢占：

```text
pending -> processing 条件更新
RowsAffected=1 才处理
```

### 18.3 消息重复与顺序

RabbitMQ 一般保证同一队列内的基本投递，但在重试、消费者失败、重新投递情况下，不能假设 exactly-once。

项目策略：

```text
不追求 exactly-once
通过幂等消费和可容忍重复实现最终正确
```

面试回答：

> 在生产系统里 MQ 通常是 at-least-once，因此业务消费端必须考虑幂等。我的项目里点赞、关注用唯一索引处理重复，评论和通知用 event_id 唯一索引，timeline 用 ZSet member 唯一性，Outbox 接受重复投递但下游可幂等。

## 19. 测试设计

### 19.1 单元测试

单元测试主要覆盖不依赖真实 MySQL/RabbitMQ 的逻辑：

```text
JWT
配置加载
Redis 封装（miniredis）
滑动窗口限流
COS key/url 生成
util 工具函数
apierror
observability
```

原则：

```text
不为了测试改业务代码
纯函数和 Redis 逻辑适合单元测试
强依赖 MySQL/RabbitMQ 的 worker/service 更适合集成测试
```

### 19.2 集成测试

集成测试使用 build tag：

```bash
go test -tags=integration ./tests/integration/...
```

依赖 Docker Compose 启动：

```text
MySQL
Redis
RabbitMQ
```

覆盖：

```text
数据库 schema
Redis 缓存、锁、限流
RabbitMQ 拓扑、重试、死信队列
Service 核心逻辑
Worker 消费
API smoke 流程
COS 少量上传/删除
```

### 19.3 性能测试

使用 k6：

```text
listLatest
listByPopularity
videoGetDetail
commentPublish
```

关注指标：

```text
QPS
P95/P99 延迟
错误率
MySQL/Redis/RabbitMQ 是否成为瓶颈
worker 消费是否积压
```

之前遇到的压测问题：

```text
VUS=20 且无 sleep，不是 20 QPS，而是 20 个虚拟用户疯狂循环
短时间打到 1600+ QPS
导致本机到虚拟机 MySQL 的连接资源耗尽
```

面试时可以讲：

> 我用 k6 做了接口压测，发现无等待循环压测会快速打满本机到远程 MySQL 的连接资源。后续把压测脚本改成更温和的默认参数，并加入脚本间冷却和失败停止，同时也意识到生产环境需要设置数据库连接池上限、监控连接数和慢查询。

## 20. 性能和生产化优化方向

当前项目已经具备比较完整的后端亮点，但如果继续生产化，可以做：

```text
1. MySQL 连接池配置：MaxOpenConns / MaxIdleConns / ConnMaxLifetime
2. API 启动不再 AutoMigrate，迁移拆成独立任务
3. RabbitMQ 积压监控和 DLX 告警
4. Redis 命中率、缓存重建次数、锁竞争次数监控
5. 结构化日志和 traceID
6. 健康检查 readiness/liveness
7. popularity worker 严格幂等
8. outbox failed 人工重试或补偿工具
9. COS 上传失败重试和上传耗时监控
10. Feed 热榜算法继续优化，例如时间衰减和多行为权重
```

## 21. 高频面试追问速答

### 21.1 你的 Feed 流怎么做的？

> 全局最新流用 Redis ZSet 存最近 1000 条热数据，member 是 videoID，score 是 create_time。请求在热区就查 Redis ZSet 拿 ID，再走多级缓存拿视频实体；请求落到冷区就查 MySQL。Redis 空时用 singleflight 重建最新 1000 条，热数据不足一页时会拼接 MySQL 冷数据。

### 21.2 多级缓存怎么防击穿？

> 视频详情 miss 时用 Redis 分布式锁，抢到锁的请求查 MySQL 并回写缓存，没抢到锁的请求短暂轮询缓存，轮询失败后才降级查库。Feed 批量实体查询用本地缓存、Redis MGet、MySQL，并用 singleflight 合并同一视频 ID 的并发查库。

### 21.3 MQ 重复消费怎么办？

> 我没有假设 MQ exactly-once，而是按业务做幂等。点赞和关注用唯一索引，重复插入忽略；取消点赞/取消关注硬删除可重复执行；评论和通知用 event_id 唯一索引；timeline 用 Redis ZSet member 唯一；热度榜目前是可容忍误差，后续可以加 event 去重。

### 21.4 Outbox 解决了什么问题？

> 解决视频写库和 MQ 投递不在同一个事务中的一致性问题。发布视频时在同一个 MySQL 事务里插入 videos 和 outbox pending 消息。OutboxWorker 后台扫描 pending，抢占为 processing，投递 MQ，成功改 published，失败重试，超过次数 failed 留库。

### 21.5 Outbox 会不会重复投递？

> 会，Outbox 本质是 at-least-once。例如 MQ 投递成功后 worker 崩溃，还没标记 published，超时后可能被重新投递。但下游 timeline 使用 ZSet，videoID 作为 member，重复写入不会产生重复视频，所以当前业务可接受。

### 21.6 死信队列怎么做的？

> 普通队列声明时绑定 `x-dead-letter-exchange=dlx.events`。消费失败后不是直接无限 requeue，而是手动 republish 并增加 `x-retry-count`，最多重试 3 次。超过后 `Nack(false,false)` 进入对应的 `.dlx` 死信队列。

### 21.7 SSE 分布式推送怎么解决？

> SSEHub 是每个 API 节点本地内存连接表。为了解决用户连接节点和消息消费节点不一致的问题，NotificationWorker 写库后把 PushMessage 发布到 Redis Pub/Sub。所有 API 节点订阅频道，收到后调用本节点 hub.Push，只有持有目标用户连接的节点会真正推送。

### 21.8 Redis Pub/Sub 会不会导致消息积压？

> 不会在没有连接的节点积压。没有目标用户连接时，hub.Push 查不到 clients[userID] 直接返回。Pub/Sub 本身只做在线广播，不负责离线可靠性；离线可靠性由 MySQL notifications 表保证。

### 21.9 Redis 挂了怎么办？

> 大部分业务降级 MySQL，例如 token 校验、视频详情、Feed、热门榜。限流会放行。通知的分布式实时推送能力会下降，但通知已经入库，可以通过列表查询。Redis 在多数场景是性能优化，不是主数据源。

### 21.10 RabbitMQ 挂了怎么办？

> API 层对点赞、评论、关注等核心写操作做了同步 fallback，尽量保证主业务可用。但 worker 异步能力、通知异步生成、timeline 派生更新会受影响。worker 进程强依赖 RabbitMQ，异常退出后依赖容器重启。

### 21.11 为什么热度榜可以不严格幂等？

> 热度榜是排序指标，不是主业务计数。重复消费造成的是分值轻微偏差，不会破坏点赞关系、评论记录这些主数据。当前业务可容忍，后续可以通过 Redis 去重 key 或 event 表实现严格幂等。

### 21.12 为什么用 COS？

> 多 API 节点下本地文件不共享，上传到某个节点后其他节点访问不到。COS 是共享对象存储，视频、封面、头像上传后返回统一 URL，数据库保存 URL，任意 API 节点都能返回同一资源。

### 21.13 为什么 API 和 worker 分进程？

> API 关注请求响应，worker 关注后台消费。拆分后可以独立扩容、独立重启、隔离故障。比如 API 流量高就扩 API，MQ 积压就扩 worker。

### 21.14 你这个项目还有什么不足？

可以诚实回答：

```text
1. MySQL 还没做读写分离和高可用
2. 生产环境不应每个 API 启动都 AutoMigrate
3. popularity worker 幂等是可选优化，当前未实现严格幂等
4. outbox failed 目前留库，还缺人工重试/补偿后台
5. 日志和监控还可以继续生产化，例如 MQ 积压、DLX、Redis 命中率、接口 P99
```

这样回答不会减分，反而说明你知道项目边界和后续方向。

## 22. 简历亮点对应关系

如果简历写：

> 设计 Redis + 本地缓存 + MySQL 多级缓存体系，优化视频详情、Feed 列表、热门榜等高频读场景

你要能讲：

```text
GetVideosByIDs: L1 本地缓存 -> L2 Redis MGet -> L3 MySQL
GetDetail: Redis 缓存 + 分布式锁防击穿
ListLatest: Redis ZSet 热数据 + MySQL 冷数据
ListByPopularity: 60 个分钟 ZSet 合并小时榜
```

如果简历写：

> 基于 RabbitMQ 实现点赞、评论、关注、热度更新和时间线投递

你要能讲：

```text
Topic exchange
routing key
主业务队列和 notification 队列绑定同一个 exchange
worker 消费手动 ack
失败 republish 重试
超过次数进 DLX
```

如果简历写：

> 引入 Outbox 模式处理视频发布时间线投递

你要能讲：

```text
本地事务写 videos + outbox
pending/processing/published/failed
claim 条件更新支持多节点抢占
processing 超时回收
at-least-once + 下游 ZSet 幂等
```

如果简历写：

> 基于事件 ID 和唯一索引实现异步消费幂等控制

你要能讲：

```text
comment.event_id unique
notification.event_id unique
like/social 使用业务唯一索引
timeline 使用 ZSet member 唯一
popularity 当前不严格幂等但可容忍
```

如果简历写：

> 基于 SSE + Redis Pub/Sub 实现分布式通知推送

你要能讲：

```text
SSEHub 是本地内存连接表
多节点下 worker 不知道用户连在哪个 API
Redis Pub/Sub 广播到所有 API
只有持有连接的节点真正 push
通知先写 MySQL，离线可查
```

## 23. 最后复习建议

面试前重点背熟这几个流程：

```text
登录鉴权流程
video/getDetail 缓存击穿流程
feed/listLatest 冷热数据流程
feed/listByPopularity 分钟窗口合并流程
video/publish Outbox 流程
worker 重试和死信流程
点赞/评论/关注幂等流程
NotificationWorker + Redis Pub/Sub + SSE 流程
COS 上传流程
Redis/RabbitMQ 故障降级
```

最容易惊艳面试官的点不是“我用了 Redis/RabbitMQ”，而是你能说清楚：

```text
为什么用
解决了什么问题
失败时怎么降级
重复消息怎么处理
多节点下哪里会出问题
当前设计有哪些取舍和不足
```

只要这些能讲清楚，这个项目就不是普通 CRUD，而是一个有工程化思考的后端项目。

