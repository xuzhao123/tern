set -e
here=$(cd "$(dirname "$0")" && pwd)
test -z "$(gofmt -l .)" || { echo "gofmt 未通过"; gofmt -l .; exit 1; }
go vet ./...
go build -o /dev/null ./...
GOOS=darwin GOARCH=arm64 go vet ./internal/sandbox/ ./internal/appserver/ . || { echo "macOS 上 go vet 未通过"; exit 1; }
GOOS=darwin GOARCH=arm64 go build -o /dev/null . || { echo "macOS 上编译未通过"; exit 1; }
python3 "$here/check.py"
