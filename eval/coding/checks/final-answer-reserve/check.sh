set -e
here=$(cd "$(dirname "$0")" && pwd)
test -z "$(gofmt -l .)" || { echo "gofmt 未通过"; gofmt -l .; exit 1; }
go vet ./...
mkdir -p /tmp/w && go build -o /tmp/w/tern .
timeout 300 python3 "$here/check.py" /tmp/w/tern "$here"
