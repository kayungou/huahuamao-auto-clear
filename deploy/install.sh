#!/usr/bin/env bash
# 在目标 Linux 服务器上执行：把当前目录的 sgz-web-linux-amd64 安装为 systemd 服务。
# 用法：sudo bash install.sh [二进制路径]
set -euo pipefail

BIN_DIR=/opt/sgz-web
SRC="${1:-$(dirname "$0")/sgz-web-linux-amd64}"
UNIT_SRC="$(dirname "$0")/sgz-web.service"

if [ "$(id -u)" != "0" ]; then
  echo "请用 root 运行（sudo bash install.sh）" >&2
  exit 1
fi
if [ ! -f "$SRC" ]; then
  echo "找不到二进制：$SRC" >&2
  exit 1
fi

install -d -m 755 "$BIN_DIR"
install -m 755 "$SRC" "$BIN_DIR/sgz-web"
install -m 644 "$UNIT_SRC" /etc/systemd/system/sgz-web.service

systemctl daemon-reload
systemctl enable --now sgz-web
sleep 1
systemctl --no-pager --lines=6 status sgz-web || true
echo "--- 健康检查 ---"
curl -fsS http://127.0.0.1:8902/api/health && echo
echo "--- 完成：http://<服务器IP>:8902/ ---"
echo "提示：本版本不落盘账号/Token。旧版遗留的 $BIN_DIR/data/yyb.db 已无用，可删除。"
