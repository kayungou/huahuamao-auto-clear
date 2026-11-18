# 安全说明

## 设计上的安全边界

- **服务端不保存账号数据**：微信 `login_buffer`、mmtls 会话只存在于进程内存，换取游戏 Token 后立即丢弃；
  服务端不写数据库、不写日志中的 Token/openid。游戏 Token 由浏览器保存在 Cookie `sgz_session` 中。
- **Token 即凭证**：拿到 Token 等同于拿到对应游戏账号的操作权限。请勿把 Token 粘贴到公开渠道，
  也不要在不可信的设备上保持登录。
- **任务隔离**：`/api/jobs/{id}` 只对提交该任务时使用的 Token 返回结果；任务 ID 顺序不可枚举他人结果。
- **扫码 Token 一次性**：`/api/qr/poll` 只在扫码确认后下发一次 Token，重复轮询不会再次返回。
- **资源上限**：扫码会话、并发任务、任务表都有上限，避免公开部署时被刷爆内存或上游接口。
- **浏览器侧**：Token 存在 JS 可读的 Cookie 中（用于界面展示），因此服务端附加
  `Content-Security-Policy`、`X-Frame-Options: DENY` 等响应头，降低 XSS / 点击劫持风险。
  项目没有第三方脚本、CDN 或远程字体。

## 已修复的审计问题（2026-11）

- **任务结果越权读取**：`/api/jobs/{id}` 原实现不需要凭证，且任务 ID 递增，任何人都可以枚举并读取他人任务结果
  （例如排行榜返回的其他玩家昵称/头像）。现在任务保存提交者 Token 的 SHA-256 指纹，读取时必须携带同一 Token，
  缺失返回 401、不匹配返回 404。
- **扫码 Token 可重复获取**：轮询接口原实现会把已下发的 Token 反复返回。现在 Token 只下发一次，重复轮询返回 410；
  前端在 `confirmed` 后立即停止轮询。
- **日志包含个人信息**：服务端日志不再打印 openid / 昵称，只记录“登录成功”。
- **资源耗尽**：扫码会话（32）、并发任务（8）、任务表（256）均有上限；请求体限制 1 MiB；HTTP 服务器配置读写超时。
- **浏览器侧加固**：新增 CSP（`default-src 'self'` 等）、`X-Frame-Options: DENY`、`Permissions-Policy`、
  `Cross-Origin-Resource-Policy`。
- **敏感数据清理**：仓库中的真实昵称、账号相关 fixture 已替换为示例数据；本地数据库、调试二进制与内部笔记不纳入版本控制。

## 公开部署建议

1. **使用 HTTPS 反向代理**（Caddy / Nginx / Cloudflare Tunnel），避免 Token 在明文 HTTP 中传输。
2. 若只给自己用，建议叠加 IP 白名单、VPN 或 HTTP Basic Auth。
3. 不要把服务端暴露为“公共代练”：任何人都只能登录自己的微信账号，但公开服务仍可能被滥用。
4. 定期升级依赖（`go get -u`、`pnpm update`），关注上游接口变更。

## 报告问题

如果发现安全问题，请不要公开提交可利用细节；请通过 GitHub 仓库的
**Security → Report a vulnerability**（私有安全公告）联系维护者。
