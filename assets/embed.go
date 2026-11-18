// Package uiassets 内嵌 Web 控制台前端产物（Vue 3 + Vite 构建的 dist/）。
//
// 产物全部随二进制打包，运行时不依赖磁盘文件、不引用任何外部 CDN，离线可用。
// 重新构建：cd frontend && pnpm install && pnpm build（或在本包执行 go generate ./assets）。
package uiassets

import "embed"

// FS 包含 dist/ 下的 index.html、assets/、favicon.svg 等构建产物。
//
//go:generate pnpm --dir ../frontend build
//go:embed dist
var FS embed.FS
