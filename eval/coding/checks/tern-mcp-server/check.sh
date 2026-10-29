set -e
here=$(cd "$(dirname "$0")" && pwd)
test -z "$(gofmt -l .)" || { echo "gofmt 未通过"; gofmt -l .; exit 1; }
go vet ./...
mkdir -p /tmp/w && go build -o /tmp/w/tern .
test -f internal/mcpserver/AGENTS.md || { echo "缺少 internal/mcpserver/AGENTS.md"; exit 1; }
grep -q 'tern mcp-server' README.md || { echo 'README 没写 tern mcp-server'; exit 1; }
timeout 120 python3 "$here/check.py" /tmp/w/tern
