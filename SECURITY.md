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

## 公开部署建议

1. **使用 HTTPS 反向代理**（Caddy / Nginx / Cloudflare Tunnel），避免 Token 在明文 HTTP 中传输。
2. 若只给自己用，建议叠加 IP 白名单、VPN 或 HTTP Basic Auth。
3. 不要把服务端暴露为“公共代练”：任何人都只能登录自己的微信账号，但公开服务仍可能被滥用。
4. 定期升级依赖（`go get -u`、`pnpm update`），关注上游接口变更。

## 报告问题

如果发现安全问题，请不要公开提交可利用细节；请通过 GitHub 仓库的
**Security → Report a vulnerability**（私有安全公告）联系维护者。
