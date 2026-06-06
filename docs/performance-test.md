# 性能测试说明

本文档记录 GoVideo 当前的接口性能测试方案。性能测试独立于集成测试，使用单独的 k6 脚本和 `.run/performance` 输出目录。

## 测试范围

当前优先压测四个核心接口：

```text
POST /feed/listLatest
POST /feed/listByPopularity
POST /video/getDetail
POST /comment/publish
```

前三个接口主要观察读路径性能，包括 MySQL、Redis 多级缓存和热门榜缓存。`/comment/publish` 是写路径接口，主要观察限流、RabbitMQ 投递、worker 消费和最终写库压力。

## 环境依赖

性能测试复用中间件 Docker Compose：

```text
deploy/docker-compose.integration.yml
```

脚本默认通过 `ssh master` 在 Linux 虚拟机中启动 MySQL、Redis、RabbitMQ，然后在本机启动 API 和 worker 进程，再执行 k6。

因为复用了同一个 compose 文件和容器名，不要和 `scripts/integration-test.sh` 同时运行。两套测试的脚本、k6/go test 逻辑和输出目录是独立的，但中间件容器不是并发隔离的。

需要本机安装：

```text
go
k6
ssh/scp
```

## 运行方式

运行全部脚本：

```bash
scripts/performance-test.sh
```

只运行某一个接口：

```bash
scripts/performance-test.sh list_latest
scripts/performance-test.sh list_by_popularity
scripts/performance-test.sh video_get_detail
scripts/performance-test.sh comment_publish
```

常用参数：

```bash
VUS=10 DURATION=1m SLEEP=0.02 scripts/performance-test.sh list_latest
VUS=5 DURATION=1m COMMENT_USERS=10 scripts/performance-test.sh comment_publish
```

默认参数比较温和：

```text
读接口: VUS=5, DURATION=30s, SLEEP=0.05
评论发布: VUS=5, DURATION=30s, SLEEP=1
all模式脚本间冷却: COOLDOWN_SECONDS=15
all模式失败后停止: STOP_ON_FAILURE=1
```

如果要逐步提高压力，建议按下面的顺序增加：

```bash
VUS=5 DURATION=30s SLEEP=0.05 scripts/performance-test.sh list_latest
VUS=10 DURATION=1m SLEEP=0.02 scripts/performance-test.sh list_latest
VUS=20 DURATION=1m SLEEP=0.01 scripts/performance-test.sh list_latest
```

不要一开始就使用 `VUS=20 SLEEP=0`。这不是 20 QPS，而是 20 个虚拟用户无等待循环请求，可能快速打满本机到虚拟机 MySQL 的连接资源。

## 输出文件

性能测试输出写入：

```text
.run/performance/
```

主要文件：

```text
list_latest-summary.json        k6 summary export
list_latest-output.txt          k6 控制台输出
```

其他脚本同理。

性能测试默认不保存 API / worker 运行日志，因为压测时 Gin 请求日志会非常大。需要排查启动失败或服务异常时，可以临时开启：

```bash
APP_LOG=1 scripts/performance-test.sh list_latest
```

开启后会覆盖写入：

```text
api.log
worker.log
```

## 注意事项

`/comment/publish` 有账号维度限流，默认每个账号每分钟 10 次。压测这个接口时，如果账号数太少，结果会主要体现限流效果，而不是写入吞吐能力。

如果要测更高写入 QPS，需要增加 `COMMENT_USERS`，或者在专门的压测配置中临时调大限流阈值。

性能测试结果受机器配置、Docker 资源、虚拟机网络和本地服务启动方式影响，不能直接代表生产环境极限性能。这个测试更适合用于定位瓶颈、比较优化前后差异，以及简历项目中的工程化展示。
