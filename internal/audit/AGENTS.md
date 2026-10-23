# audit 的约束

- 审计日志 `.data/audit/audit.jsonl` 只追加、哈希链、文件锁、写后 fsync；参数只存哈希；只读工具不记。
- 高风险确认与联网审批写不进审计就不执行。
