#!/bin/sh
# PostToolUse（edit_file、write_file、apply_patch）：改过的 .go 文件直接 gofmt -w。harness 把这次改写算进写入（快照、读取账本跟着更新），
# 并把改动片段告诉模型；gofmt 报错（语法错误）时把错误反馈给模型。
errors=""
paths=$(jq -rf "$AGENT_HARNESS_DIR/hooks/edit-paths.jq")
while IFS= read -r path; do
  case "$path" in *.go) ;; *) continue ;; esac
  file="$AGENT_PROJECT_DIR/$path"
  [ -f "$file" ] || continue
  if ! out=$(gofmt -l "$file" 2>&1); then
    errors="$errors
$out"
  elif [ -n "$out" ]; then
    gofmt -w "$file"
  fi
done <<EOF
$paths
EOF
[ -z "$errors" ] || jq -n --arg r "gofmt 报错：$errors" '{decision: "block", reason: $r}'
