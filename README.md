# ScoreBot-Go / Web 版（猫猫头的七天）

> 下面是 Web 版说明。原命令行 / QQ Bot 版的说明保留在文末「原版 ScoreBot-Go 说明」之后。

Web 版把原来的机器人命令流改成一个**单页网页应用**：浏览器里登录、绑定查分账号、看成绩趋势和考试记录，不再需要一条条发命令。

- 品牌：**猫猫头的七天**（成绩查询 · ScoreBot）
- 登录方式：**司白画云盘** OAuth（授权码流），首次登录自动创建账户
- 界面：黑白灰极简单页，原生 HTML/CSS/JS，零依赖、无 CDN

## 功能

- **OAuth 登录**：授权码流 + HMAC-SHA256 签名 Cookie 会话，密钥存于服务端文件（600）
- **多平台账号并存**：七天网络 / 好分数学生版 / 好分数家长版 / 百分智，绑定新平台时自动把旧账号存档到 `bound_accounts`，可一键切换，不会丢号
- **成绩总览**：考试场次、最近得分（含涨跌）、最高分、平均分四个统计卡
- **成绩趋势图**：月考（黑实线）与周测（灰虚线）两条趋势线，纯手写 SVG
- **考试记录**：点击任意一行展开该场考试的科目明细
- **未认领考试**：列出待认领考试，选择学号后一键认领
- **设置**：绑定 / 切换 / 解绑查分账号；解绑后服务器上的账号密码立即清除

## 目录结构

```text
web_server.go      HTTP 服务与路由（/api/ 与 /sbapi/ 双前缀）
web_auth.go        OAuth 配置、登录回调、会话签名校验
web_api.go         业务接口：exams / exam_detail / profile / bind / unbind / switch / claim
web_chat.go        WebChatSender：把原命令输出收集成 JSON 给前端
web/index.html     前端单页（原生 HTML/CSS/JS，单文件）
provider_qt.go     七天网络接口适配（my 与 score 两个域名要求不同的 Version 头）
deploy/            脱敏后的 nginx / systemd 配置示例
```

## 快速开始

```bash
# 1. 编译（部署机内存小的话，可在别的机器上交叉编译后传过去）
GOOS=linux GOARCH=amd64 go build -o scorebot .

# 2. 准备运行目录
mkdir -p /opt/scorebot
cp scorebot /opt/scorebot/
head -c 32 /dev/urandom | base64 > /opt/scorebot/session.key
chmod 600 /opt/scorebot/session.key

# 3. 写 OAuth 配置（示例见 deploy/oauth.env.example）
cp deploy/oauth.env.example /opt/scorebot/oauth.env
chmod 600 /opt/scorebot/oauth.env
$EDITOR /opt/scorebot/oauth.env

# 4. 安装服务（示例见 deploy/scorebot-web.service）
cp deploy/scorebot-web.service /etc/systemd/system/
systemctl daemon-reload && systemctl enable --now scorebot-web

# 5. 前端与反代（示例见 deploy/nginx.conf.example）
cp web/index.html <站点根目录>/index.html
```

## 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `CHAT_ADAPTER` | 必须为 `web` | `cli` |
| `DATA_STORE` | 存储后端，推荐 `json` | `json` |
| `JSON_STORE_PATH` | JSON 存储文件路径 | `data.json` |
| `WEB_ADDR` | HTTP 监听地址 | `127.0.0.1:8787` |
| `WEB_DIR` | 静态首页目录（留空则由 nginx 提供） | 空 |
| `SESSION_KEY_FILE` | 会话签名密钥文件 | `/opt/scorebot/session.key` |
| `OAUTH_CLIENT_ID` | OAuth 客户端 ID | 空（留空则关闭登录） |
| `OAUTH_CLIENT_SECRET` | OAuth 客户端密钥 | 空 |
| `OAUTH_AUTHORIZE_URL` | 授权端点 | 空 |
| `OAUTH_TOKEN_URL` | Token 端点 | 空 |
| `OAUTH_USERINFO_URL` | 用户信息端点 | 空 |
| `OAUTH_REDIRECT_URI` | 回调地址 | 空 |
| `OAUTH_SCOPE` | 授权范围 | `openid email profile` |

## 接口

所有接口同时挂在 `/api/` 与 `/sbapi/` 两个前缀下（后者用于在 CDN 已缓存旧响应时绕开）。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `health` | 健康检查 |
| GET | `auth/me` | 当前登录状态 |
| GET | `auth/login` | 跳转 OAuth 授权 |
| POST | `auth/callback` | 授权回调，换 token 并建立会话 |
| POST | `auth/logout` | 退出登录 |
| GET | `profile` | 当前绑定信息 + 已绑定账号列表 |
| POST | `bind` / `unbind` / `switch` | 绑定 / 解绑 / 切换查分账号 |
| GET | `exams` | 考试列表（含统计字段） |
| GET | `exam/detail?guid=` | 单场考试与科目明细 |
| GET | `unclaimed` | 未认领考试 |
| POST | `claim` | 认领考试 |
| POST | `command` | 原始命令通道（供调试） |

## 已知限制

- **科目明细**：七天网络的科目明细接口受会员权限限制，非会员账号只能拿到总分，前端会显示相应提示。这是平台侧限制，接口层无法绕过。
- 七天网络为单设备登录，网页端查询时若手机 APP 在线，可能出现互踢。

---

# 原版 ScoreBot-Go 说明


**————散是满天星。如果现在不做些什么那我岂不是很亏？**

用 Go 编写的成绩查询机器人后端，默认运行方式为本地 CLI + JSON 文件存储。

> 注意：本项目会处理账号、密码、token、姓名、学校、学号、考试成绩和答题卡等敏感信息。公开部署、二次开发或提供服务前，请确认已经获得用户授权，并自行承担数据安全、合规和第三方平台服务条款风险。
> 本项目遵循 [MIT License](LICENSE) ，仅供学习、研究使用。您利用此代码（包括但不限于使用、复制、传播、分发等），即代表您已阅读、理解并同意：开发者无法预测您的行为，您必须负有因此而产生的包括但不限于法律责任的相关责任。

## 功能概览

- 好分数、七天网络、百分智平台的账号绑定及信息获取。
- 好分数、七天网络平台的成绩相关查询，包括：考试列表获取、考试详情获取、科目详情、答题详情获取、答题卡卡面图片获取、排名及均分数据获取（需要教师版账号）。
- 使用 QQ Bot 适配器和命令行 CLI Chat 适配器。
- 存储格式：MySQL、SQLite、JSON 和 内存。

## 项目架构

项目分为三层：

- 命令核心：`command_*.go`，解析命令、执行业务逻辑。
- Chat 适配器：对接不同聊天平台，负责消息的接收与发送。
- Store 适配器：对接不同存储后端，负责用户数据、缓存和消息去重。

默认运行方式：

```text
CLI 终端输入 -> MessageContext -> CommandHandler -> CLIChatSender + JSONStore
```

需要 QQ Bot 时，设置 `CHAT_ADAPTER=fc` 切换为：

```text
阿里云 FC / QQ Bot 事件 -> MessageContext -> CommandHandler -> QQChatSender + DataStore
```

## 运行及部署

### 环境要求

- Go 1.25.4 或兼容版本
- JSON / SQLite Store 无需外部依赖，开箱即用
- MySQL 8.x，仅使用 MySQL Store 时需要
- 阿里云函数计算 FC + `build-fc-zip.exe`，仅部署 QQ Bot 时需要

### 环境变量

#### Chat 入口

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `CHAT_ADAPTER` | 空 | 空值或任意非 `fc` 值走 CLI；设置为 `fc` 进入 FC + QQBot 模式 |
| `CLI_USER_ID` | `cli-user` | CLI 模式下的用户 ID |
| `CLI_USER_NAME` | `CLI User` | CLI 模式下的用户名 |
| `CLI_CONVERSATION_ID` | `cli` | CLI 模式下的会话 ID |

#### Store

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DATA_STORE` | `json` | 可选 `json`、`sqlite`、`memory`、`mysql` |
| `JSON_STORE_PATH` | `data.json` | JSON Store 文件路径 |
| `SQLITE_STORE_PATH` | `data.sqlite` | SQLite Store 文件路径 |
| `DB_USER` | 无 | MySQL 用户名 |
| `DB_PASSWORD` | 无 | MySQL 密码 |
| `DB_HOST` | 无 | MySQL 主机 |
| `DB_PORT` | 无 | MySQL 端口 |
| `DB_NAME` | 无 | MySQL 数据库名 |

#### QQ Bot

| 变量 | 说明 |
| --- | --- |
| `qqbot_appId` | QQ Bot App ID |
| `qqbot_clientSecret` | QQ Bot Client Secret |

#### 远端通知

| 变量 | 说明 |
| --- | --- |
| `MOON_NOTIFY_ENDPOINT` | 通知接口地址 |
| `MOON_NOTIFY_BEARER_TOKEN` | Bearer Token |
| `MOON_NOTIFY_GROUP_ID` | 通知群 ID |

### 本地运行

直接启动即可进入 CLI 交互模式：

```powershell
go run .
```

```text
CLI chat started. Type /exit to quit.
> /帮助
> /绑定账号 1 账号 密码
> /查询
```

退出 CLI：

```text
/exit
```

#### Store 切换

默认使用 JSON 文件存储（`data.json`），可通过环境变量切换：

```powershell
# SQLite 存储
$env:DATA_STORE="sqlite"
go run .

# 纯内存存储（不落盘）
$env:DATA_STORE="memory"
go run .

# MySQL 存储
$env:DATA_STORE="mysql"
$env:DB_HOST="127.0.0.1"
$env:DB_PORT="3306"
$env:DB_USER="root"
$env:DB_PASSWORD="..."
$env:DB_NAME="qqbot"
go run .
```

#### 切换到 QQ Bot 模式

```powershell
$env:CHAT_ADAPTER="fc"
$env:qqbot_appId="..."
$env:qqbot_clientSecret="..."
go run .
```

#### 教师端分析（好分数）

查询 `/考试详情` 时，除了学生端的基本成绩，还可以展示教师端数据：年级/班级/联考排名、均分、最高分以及各科分析。这需要预先在 Store 中录入教师账号。

**步骤一**：首次启动 CLI 生成 `data.json`，退出后编辑：

```json
{
  "users": {},
  "teachers": {
    "测试一中": {
      "school": "测试一中",
      "account": "账号",
      "password": "密码",
      "tofenxi": ""
    }
  }
}
```

`school` 必须与学生绑定后好分数返回的学校名完全一致。

**步骤二**：启动并绑定学生账号，然后查询考试详情：

```text
> /绑定账号 1 账号 密码
 * 绑定成功！
用户基本信息：[测试一中]张三(20240001)

> /查询
[2024] 期中考试 ID: 1234567

> /考试详情 1234567
===== 考试概览 =====
总分 620 / 750

===== 考试数据 =====
- 总分 620 [联45|校15|班3]
参考人数 | 联 3200 校 800 班 45
平均分数 | 联 590 校 580 班 562
最高分数 | 联 735 校 720 班 685

===== 个人数据 =====
语文 105 [校30|班5]
数学 128 [校8|班1]
...
```

首次查询时系统检测到教师 cookie 为空，会用预置的账号密码自动登录教师端获取凭据，后续查询直接复用。若凭据过期也会自动重登刷新。

教师账号的 `tofenxi` 字段可选：

- 留空或不设置：通过教师平台获取数据（如上所示）
- 设为 `"TRUE"`：通过分析平台获取数据，需要账号有对应权限（例如： `校领导` 角色）
- 设为 `"FAILED"`：不使用该账号获取数据

#### 教师端分析（七天网络）

查询 `/考试详情` 时，除了学生端的基本成绩和等第，还可以展示教师端数据：联考/全校/班级的排名、考试人数、均分、最高分，以及赋分报告（物理/历史组排名）。这需要预先在 Store 中录入教师账号。

**步骤一**：编辑 `data.json`，在 `teachers` 中添加以 `QT-` 开头的条目：

```json
{
  "users": {},
  "teachers": {
    "QT-测试一中": {
      "school": "QT-测试一中",
      "account": "账号",
      "password": "密码",
      "login_mode": "web",
      "tofenxi": ""
    }
  }
}
```

`school` 字段必须带 `QT-` 前缀，名称使用规范化后的学校名（与代码内 `schoolMapping` 中的映射目标一致，例如 `测试一中` 而非 `测试县第一中学`）。

**步骤二**：启动并绑定七天网络学生账号，然后查询考试详情：

```text
> /绑定账号 4 账号 密码
 * 绑定成功！（七天）
用户基本信息：张三

> /查询
【七天网络考试列表】
[2024] 期中考试 ID: 123456

> /考试详情 123456
===== 考试概览 =====
总分 620 / 750
> 年级人数 800 | 等第 B3
> 预估年级排名 224-288

- 语文 105 [联320|校30|班3]
考试人数 | 联 3200 校 800 班 45
平均分数 | 联 106 校 104 班 102
最高分数 | 联 135 校 128 班 120

- 数学 128 [联80|校8|班1]
...

===== 物理/历史类排名 =====
- 总分 620 [县15|校3]
...
```

**登录模式**：系统默认使用 Web 模式登录教师端，若被顶号则自动回退到 APP 模式。登录成功后，根据学校名匹配租户列表，取学校名完全一致的租户进入。如需手动指定登录模式，则需要编辑 `data.json` 中对应教师条目的 `login_mode` 字段：

| 值 | 说明 |
| --- | --- |
| `"web"` 或不设置 | Web 端登录（默认），token 有效期较短、约 6 小时 |
| `"app"` | APP 端登录，token 有效期较长 |

**分析规则**：通过考试规则列表自动选择分析规则。若规则类型为"新高考赋分制"（RuleType=2），会额外查询物理组或历史组的排名报告。

**凭证管理**：登录成功后将 token、租户码、登录模式写入 Store，后续查询直接复用。若 token 过期或被顶号，系统自动重登刷新；若两次尝试后仍失败或凭据无效，则标记 `tofenxi` 为 `"FAILED"` 并推送给管理员，后续不再尝试。

> **注意**：七天网络教师端同一账号在同一时间、同一平台（Web/APP）内仅允许一台设备登录。使用相关功能时可能会将他人的正常登录状态踢下线。

### 本地部署（Windows / Linux）

项目默认即可本地运行，无需 MySQL 或云服务。

#### 构建

```powershell
# Windows（当前系统直接构建）
go build -trimpath -ldflags="-s -w" -o scorebot.exe .
```

在 Windows 上交叉编译 Linux 二进制：

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -trimpath -ldflags="-s -w" -o scorebot .; $env:GOOS=""; $env:GOARCH=""; $env:CGO_ENABLED=""
```

> **注意**：`$env:GOOS` 等环境变量会残留在当前 PowerShell 会话中。交叉编译后若需切回 Windows 构建，请执行 `$env:GOOS=''; $env:GOARCH=''; $env:CGO_ENABLED=''` 清除，或重新打开终端。

#### 运行

```bash
# 使用默认 JSON 存储
./scorebot

# 使用 SQLite 存储
DATA_STORE=sqlite ./scorebot

# 指定数据文件路径
JSON_STORE_PATH=/var/data/scorebot.json ./scorebot
```

#### 系统服务（Linux systemd）

```ini
[Unit]
Description=ScoreBot-Go CLI
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/scorebot
ExecStart=/opt/scorebot/scorebot
Environment="DATA_STORE=sqlite"
Environment="SQLITE_STORE_PATH=/opt/scorebot/data.sqlite"
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

#### 可行性说明

| 项目 | Windows | Linux |
| --- | --- | --- |
| CLI 交互模式 | 原生支持，pwsh / cmd / Windows Terminal | 原生支持，任何终端 |
| JSON Store | 单文件读写，无需外部依赖 | 同左 |
| SQLite Store | 纯 Go 驱动（modernc.org/sqlite），无 CGO | 同左，交叉编译无问题 |
| Memory Store | 进程内内存，无依赖 | 同左 |
| 编译产物 | 单 `.exe` 文件 | 单二进制文件 |

**局限：**
- 成绩查询需访问第三方平台 API（好分数、七天网络、百分智），需要互联网连接。
- QQ Bot 模式需要阿里云 FC 部署环境和已注册的 QQ Bot 应用。
- 无内置 HTTP API 或守护进程化，如有需要可配合 nginx/系统服务使用。

### FC 部署（QQ Bot）

阿里云函数计算 FC 的 Linux 构建命令：

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -trimpath -ldflags="-s -w" -o main .; $env:GOOS=""; $env:GOARCH=""; $env:CGO_ENABLED=""
& "$env:USERPROFILE\go\bin\build-fc-zip.exe" -output main.zip main
```

### 数据库迁移（仅 MySQL）

使用 MySQL Store 时，部署前先执行：

```text
migrations/20260505_runtime_schema.sql
```

JSON / SQLite Store 启动时会自动建表，无需手动迁移。

## 常用命令

```text
/绑定账号 [版本] [账号] [密码]
/取消绑定
/获取快照
/查询
/考试详情 [考试ID]
/答题卡 [科目ID-短号]
/科目详情 [科目ID-短号]
/错题详情 [科目ID-短号]
/答题详情 [科目ID-短号]
/帮助
```

## 基于本项目二次开发

### 扩展 Chat 适配器

实现 `ChatSender`：

```go
type ChatSender interface {
	SendText(ctx context.Context, msg *MessageContext, content string) map[string]any
	SendImage(ctx context.Context, msg *MessageContext, imageContent []byte, content string) map[string]any
	SendImageReader(ctx context.Context, msg *MessageContext, imageContent io.Reader, content string) map[string]any
}
```

然后在 `main.go` 或自己的入口中注入：

```go
handler := newCommandHandler(yourSender)
handler.handle(msgCtx)
```

现有实现：

- `QQChatSender`：QQ Bot 发送文本和图片。
- `CLIChatSender`：命令行输出文本，图片写入临时 PNG 文件并打印路径。

### 扩展 Store

实现 `DataStore` 接口：

```go
type DataStore interface {
    ViewUser(userKey string) map[string]any
    NewUser(userKey string)
    WriteUser(userKey string, data map[string]any)
    DeleteUser(userKey string)
    // ... 以及教师数据、缓存、消息去重等方法，见 data_store.go
}
```

然后在 `main.go` 的 `configureRuntimeFromEnv()` 中注册即可。

现有实现：

- `JSONStore`（默认）：单 JSON 文件持久化，零依赖，适合本地使用。
- `SQLiteStore`：SQLite 文件数据库，完整 SQL 支持，适合单机部署。
- `MySQLStore`：MySQL 数据库，适合 FC 云部署和多实例共享。
- `MemoryStore`：进程内内存，适合调试和演示，重启后数据丢失。

## 安全提示

- 不要提交真实 `.env`、数据库导出、用户数据、token 或部署密钥。
- 本项目当前仍会保存用户绑定信息，包含账号、密码和 token。用于公开服务前，建议先实现加密存储、最小化保存和数据删除审计。
- 第三方平台接口可能受平台规则、风控策略和服务条款限制，请自行确认使用边界。
