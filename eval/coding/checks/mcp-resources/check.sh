# 在可写的临时副本里放进校验 server 与测试，按工具名调用两个资源工具；工作区本身只读、不改。
set -e
here=$(cd "$(dirname "$0")" && pwd)
rm -rf /tmp/w && mkdir -p /tmp/w && cp -a . /tmp/w/repo && cd /tmp/w/repo
test -z "$(gofmt -l internal main.go)" || { echo "gofmt 未通过"; gofmt -l internal main.go; exit 1; }
go vet ./internal/mcp ./internal/agent .
mkdir -p internal/evalsrv && cp "$here/server.go.txt" internal/evalsrv/main.go && go build -o /tmp/w/srv ./internal/evalsrv
cp "$here/resources_test.go.txt" internal/agent/zz_eval_resources_test.go
EVAL_MCP_SERVER=/tmp/w/srv go test ./internal/agent -run TestEvalMCPResources -count=1
go build -o /tmp/w/tern . && /tmp/w/tern -mcp-server /tmp/w/srv -mcp-list > /tmp/w/list.txt 2>&1
grep -iqE 'resources.{0,8}3' /tmp/w/list.txt || { echo "-mcp-list 没有列出 resources 数目"; cat /tmp/w/list.txt; exit 1; }
grep -rqs '"list_mcp_resources"' --include=*.go internal main.go
