# 部署示例

本目录下的文件都是**脱敏后的模板**，服务器 IP、root 密码、真实 OAuth 密钥、真实域名均已剔除，
请按自己的环境替换后再使用。

| 文件 | 放到哪里 | 说明 |
| --- | --- | --- |
| `nginx.conf.example` | 宝塔站点配置 / `/etc/nginx/conf.d/` | 站点 80/443，静态首页 + `/api/`、`/sbapi/` 反代到 8787 |
| `scorebot-web.service` | `/etc/systemd/system/scorebot-web.service` | systemd 单元，OAuth 密钥走 `EnvironmentFile` |
| `oauth.env.example` | `/opt/scorebot/oauth.env`（权限 600） | OAuth 配置，替换成自己的 client id / secret |

## 部署步骤

```bash
# 1. 二进制
GOOS=linux GOARCH=amd64 go build -o scorebot .
install -m 0755 scorebot /opt/scorebot/scorebot

# 2. 会话签名密钥（务必自备，不要复用示例）
head -c 32 /dev/urandom | base64 > /opt/scorebot/session.key
chmod 600 /opt/scorebot/session.key

# 3. OAuth 配置
cp deploy/oauth.env.example /opt/scorebot/oauth.env
chmod 600 /opt/scorebot/oauth.env
$EDITOR /opt/scorebot/oauth.env

# 4. systemd
cp deploy/scorebot-web.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now scorebot-web
systemctl status scorebot-web

# 5. 前端
cp web/index.html <站点根目录>/index.html

# 6. nginx 反代（替换域名后）
cp deploy/nginx.conf.example /etc/nginx/conf.d/grade.conf
nginx -t && systemctl reload nginx
```

## 注意事项

- 后端只监听 `127.0.0.1:8787`，**不要**直接暴露到公网，必须走 nginx + HTTPS。
- 会话 Cookie 使用 HMAC-SHA256 签名，密钥来自 `SESSION_KEY_FILE`；密钥丢失会强制所有人重新登录。
- 如果前面挂了 CDN（Cloudflare / EdgeOne 等），改动后端路由前缀后要记得刷新缓存，
  这也是本项目同时注册 `/api/` 与 `/sbapi/` 两个前缀的原因。
- 用户查分账号密码保存在 `JSON_STORE_PATH` 指向的文件里（默认 `/opt/scorebot/data.json`），
  该文件已在 `.gitignore` 中排除，切勿提交到仓库。
