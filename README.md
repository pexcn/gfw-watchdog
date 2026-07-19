# gfw-watchdog

`gfw-watchdog` 是一个使用 Go 编写的常驻 IP 可达性监控程序。它通过 TCP、UDP 和 ICMP 探测 IPv4/IPv6 地址，在状态发生变化时通过 Telegram 或企业微信机器人发送通知。

本项目使用随机探测间隔、连续失败阈值和可选对照组，尽量降低瞬时丢包、本地网络故障或跨境链路异常造成的误报。

> [!IMPORTANT]
> `gfw-watchdog` 根据端到端可达性和对照组状态进行启发式判断，无法从数据包层面证明故障一定由 GFW 导致。建议配置可靠的国内与海外对照目标，并结合其他观测结果判断。

## 特性

- 支持 IPv4 和 IPv6
- 支持 TCP Connect、UDP Echo 和 ICMP Echo 探测
- 每个 IP 可独立配置多个协议和端口
- 每个探测目标使用独立 goroutine 和随机探测间隔
- 连续失败后判定为不可达，一次成功即可判定恢复
- 被阻断目标自动进入长冷却周期，出现成功迹象后恢复正常探测频率
- 支持对照组，辅助区分目标异常与本地网络/跨境链路异常
- 支持 Telegram Bot 和企业微信机器人通知
- 通知异步发送，带独立队列、指数退避重试和优雅关闭
- 附带独立 UDP Echo Server
- 纯 CLI Flag 驱动，无配置文件依赖
- 二进制不依赖 CGO

## 工作原理

每个 `IP × 协议 × 端口` 组合都是一个独立探测目标。程序启动后立即探测一次，随后在指定区间内随机采样下一次等待时间。

目标有三种内部状态：

```text
unknown -> ok <-> blocked
```

- 连续失败达到 `--fall` 后进入 `blocked`，默认需要 3 次。
- 连续成功达到 `--rise` 后进入 `ok`，默认需要 1 次。
- 从 `unknown` 首次确定状态只建立基线，不发送通知。
- 普通目标持续处于 `blocked` 时使用 `--blocked-cooldown`。
- `blocked` 目标一旦探测成功，立即恢复使用普通间隔；默认 `--rise=1`，同时确认恢复。
- 对照目标始终使用普通间隔，且不会为自身状态变化发送通知。

目标进入 `blocked` 时，事件分类如下：

| 对照组状态 | 事件 | 含义 |
|---|---|---|
| 未配置 | `blocked` | 目标不可达，但无法排除本地网络问题 |
| 至少一个对照目标正常 | `blocked` | 目标异常，而对照链路仍可用 |
| 所有对照目标均异常 | `network_issue` | 更可能是本地网络或链路整体异常 |
| 目标重新成功 | `recovered` | 目标恢复可达 |

## 要求

- Linux
- Go 1.26 或更高版本（从源码构建）
- GNU Make（使用 Makefile 时）
- ICMP 探测需要非特权 ping socket 或 `CAP_NET_RAW`，详见 [ICMP 权限](#icmp-权限)

## 构建与安装

构建 `gfw-watchdog` 和 `echo-server`：

```bash
make
```

运行测试：

```bash
make test
```

安装到 `/usr/local/bin`：

```bash
make install
```

也可以指定安装目录：

```bash
make install BINDIR="$HOME/.local/bin"
```

清理构建产物和 Go 缓存：

```bash
make clean
```

## 快速开始

仅探测一个 IPv4 地址的 ICMP：

```bash
gfw-watchdog --ip 203.0.113.10
```

探测 TCP 端口并配置对照组：

```bash
gfw-watchdog \
  --ip 203.0.113.10:443/tcp \
  --control 192.0.2.10:443/tcp \
  --control 198.51.100.10:443/tcp
```

同时探测 TCP、UDP 和 ICMP：

```bash
gfw-watchdog \
  --ip 203.0.113.10:443/tcp,9000/udp,icmp \
  --control 192.0.2.10:443/tcp,icmp \
  --control 198.51.100.10:443/tcp,icmp
```

## 目标语法

`--ip` 和 `--control` 均可重复指定，格式为：

```text
IP[:item1,item2,...]
```

每个 item 可以是：

| item | 探测方式 |
|---|---|
| `icmp` | ICMP Echo |
| `PORT` | TCP，等价于 `PORT/tcp` |
| `PORT/tcp` | TCP Connect |
| `PORT/udp` | UDP Echo |

示例：

```text
--ip 1.1.1.1
--ip 1.1.1.1:icmp
--ip 1.1.1.1:80
--ip 1.1.1.1:80/tcp
--ip 1.1.1.1:9000/udp
--ip 1.1.1.1:80/tcp,80/udp
--ip 1.1.1.1:80,443,icmp
--ip [2001:db8::1]:443/tcp,9000/udp
--ip 2001:db8::1
```

注意事项：

- 只接受 IP 字面量，不支持域名、CIDR、IP 范围或服务名。
- 裸 IP 默认只探测 ICMP。
- IPv6 带 item 列表时必须使用方括号；裸 IPv6 不需要方括号。
- 只探测明确列出的协议，不会自动附加其他探测。
- 端口范围为 1–65535。
- 协议名必须使用小写 `tcp`、`udp`、`icmp`。
- 重复 item 和同组内重复目标会被去重。
- 请勿将完全相同的 IP、协议和端口同时配置为普通目标与对照目标。

## 命令行参数

| 参数 | 默认值 | 说明 |
|---|---:|---|
| `-i`, `--ip spec` | 必填 | 普通探测目标，可重复 |
| `-c`, `--control spec` | 无 | 对照目标，可重复 |
| `-I`, `--interval min-max` | `60s-120s` | 正常状态的随机探测间隔 |
| `-b`, `--blocked-cooldown min-max` | `12h-24h` | 普通目标确认不可达后的随机探测间隔 |
| `-r`, `--rise n` | `1` | 判定恢复所需的连续成功次数 |
| `-f`, `--fall n` | `3` | 判定不可达所需的连续失败次数 |
| `-t`, `--timeout duration` | `5s` | 单次探测超时 |
| `-w`, `--webhook spec` | 无 | 通知目标，可重复 |
| `-h`, `--help` | / | 显示帮助并退出 |

时间使用 Go duration 格式，例如 `500ms`、`30s`、`10m`、`12h`。区间必须写成 `min-max`；两端相等时等价于固定间隔。

短参数支持以下形式：

```text
-i VALUE
-iVALUE
-i=VALUE
```

## TCP、UDP 与 ICMP 探测

### TCP

TCP 探测只建立连接并立即关闭，不发送任何应用层 payload。连接成功即视为探测成功。

因此，它只表示 TCP 端口可以完成连接，不代表端口上的应用协议工作正常。

### UDP

UDP 本身没有握手，普通 UDP 服务也不一定会响应未知数据。为了获得可验证结果，`gfw-watchdog` 会发送随机 8 字节 payload，并要求服务端原样返回。

请在目标主机上运行配套 echo-server：

```bash
echo-server -listen :9000
```

然后在监控端配置：

```bash
gfw-watchdog --ip 203.0.113.10:9000/udp
```

> [!WARNING]
> `echo-server` 不提供认证、加密、访问控制或速率限制。部署到公网时应使用防火墙仅允许监控端 IP 访问，避免形成公开 UDP 回显服务。

UDP Echo 端口应与 Shadowsocks、V2Ray 等真实服务端口分开。向这些服务发送未知 payload 通常会被静默丢弃，无法用于可靠的通用 UDP 可达性判断。

### ICMP 权限

程序仅在实际配置了 ICMP 目标时检测 ICMP 能力，并优先使用 Linux 非特权 ping socket。若不可用，则回退到 raw socket。

可以先检查当前 ping group 范围：

```bash
sysctl net.ipv4.ping_group_range
```

如果运行环境不允许非特权 ping socket，可为二进制授予 capability：

```bash
sudo setcap cap_net_raw=+ep /usr/local/bin/gfw-watchdog
```

也可以通过具备 `CAP_NET_RAW` 的 systemd 服务或容器运行。未配置 ICMP 目标时不需要这些权限。

## 对照组建议

建议至少配置两类由你自行验证过的对照目标：

1. 国内锚点，用于判断本机或本地网络是否正常。
2. 海外锚点，用于判断跨境链路是否整体正常。

只有当所有对照目标均不处于正常状态时，普通目标的失败事件才会被标记为 `network_issue`。对照目标应长期稳定，并尽量使用与普通目标相同的探测协议。

程序启动后，对照目标建立正常基线前处于 `unknown`。如果普通目标在此期间先达到失败阈值，所有对照目标均非 `ok`，事件可能暂时被分类为 `network_issue`。

## Webhook 通知

支持以下通知渠道：

- Telegram Bot
- 企业微信机器人

每个通知目标格式为：

```text
type=telegram|wecom,url=URL[,name=NAME]
```

`name` 可选，仅用于日志识别。可以重复传入 `--webhook`：

```bash
gfw-watchdog \
  --ip 203.0.113.10:443 \
  --webhook 'type=telegram,url=https://api.telegram.org/bot<TOKEN>/sendMessage?chat_id=<CHAT_ID>,name=telegram' \
  --webhook 'type=wecom,url=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=<KEY>,name=wecom'
```

Telegram 的 URL 必须包含 `chat_id` 查询参数。

> [!CAUTION]
> Telegram Bot Token 和企业微信机器人 Key 都属于敏感信息。通过命令行传递时，它们可能出现在 Shell history 和进程列表中。生产环境建议使用 `WEBHOOKS` 环境变量，并限制环境文件的读取权限。

### 使用 `WEBHOOKS` 环境变量

环境变量中的通知目标会先加载，随后追加所有 `--webhook` 参数。

多行 `key=value` 格式：

```bash
export WEBHOOKS='type=telegram,url=https://api.telegram.org/bot<TOKEN>/sendMessage?chat_id=<CHAT_ID>,name=telegram
type=wecom,url=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=<KEY>,name=wecom'
```

YAML 风格列表：

```yaml
- name: "telegram"
  type: telegram
  url: "https://api.telegram.org/bot<TOKEN>/sendMessage?chat_id=<CHAT_ID>"
- name: "wecom"
  type: wecom
  url: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=<KEY>"
```

这是一种轻量 YAML 风格格式，并非完整 YAML 解析器。仅支持由 `-` 开始的条目及 `name`、`type`、`url` 字段。

### 发送行为

- 每个 webhook 使用独立 goroutine 和容量为 256 的内存队列。
- 通知不会阻塞探测循环。
- 队列满时丢弃新事件并记录日志。
- 单次发送最多尝试 4 次，重试间隔为 1、2、4 秒。
- HTTP 2xx 视为成功；企业微信还要求响应中的 `errcode` 为 0。
- 队列不持久化，进程异常终止时可能丢失待发送事件。

## 日志与退出

- 日志输出到 stdout，适合由容器运行时收集。
- 每次探测成功或失败、状态变化及通知错误都会记录日志。
- 配置错误退出码为 2，启动阶段致命错误退出码为 1。
- 收到 `SIGINT` 或 `SIGTERM` 后停止调度，等待探测 goroutine 结束，并给通知队列最多约 5 秒的排空窗口。
- 被墙状态不会通过进程退出码表达；正常工作时程序应持续运行。
