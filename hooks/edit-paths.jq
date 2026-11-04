# 这次编辑涉及的路径，一行一个：edit_file、write_file 的 path；apply_patch 补丁里的文件头（同 permission.patchPaths）。
if .tool_input.path then .tool_input.path
else (.tool_input.input // "") | split("\n")[]
  | capture("^\\*\\*\\* (Add File|Delete File|Update File|Move to): (?<p>.*)$").p
  | gsub("^\\s+|\\s+$"; "")
end
