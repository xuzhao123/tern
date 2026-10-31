set -e
here=$(cd "$(dirname "$0")" && pwd)
test -z "$(gofmt -l .)" || { echo "gofmt 未通过"; gofmt -l .; exit 1; }
go vet ./...
go build -o /dev/null .
python3 "$here/check.py"
