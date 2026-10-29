# 文件级 Git 交付

2026-10-28，WORK-04/A20。提供读取状态、按文件暂存/取消暂存和本地提交；不提供push、PR、hunk或重写历史，不自动提交开发中的Tern仓库。

- `workspace/git/state|act|operation`：任务快照中的实际目录必须等于Git工作树根。版本包含HEAD/分支、index内容hash、状态及可读取变更文件的内容hash。写入前重新读取版本；冲突/锁/受限文件给明确错误，保留用户输入。
- 暂存/取消暂存在独立临时index中准备，持Git原生index.lock；准备后再次核对工作文件、HEAD、真实index版本。只有一致才原子替换真实index；不改工作目录文件。目录/子模块、特殊文件、自定义clean/process过滤器和无法完整核对的超大文件暂不在网页写入范围。
- 暂存使用无过滤的对象写入和index plumbing，不调用git add，避免配置在预览后变化时执行外部clean/process。需要text/eol/working-tree-encoding/ident转换或core.autocrlf的文件给出不支持原因，使用原Git流程；不静默改变行尾。更新ref的子进程强制空hooks路径，预检仍拒绝原配置中的可执行钩子。
- 提交针对完整已暂存树，先显示目录、命名分支、作者、文件和提交说明。使用commit-tree创建对象，持有原生index.lock并再次核对HEAD后，update-ref以旧OID做CAS，仅更新审核时的固定命名分支。比较冲突不更新引用。失败可能留下Git可回收的未引用对象，不改工作目录。外部直接修改HEAD不受index.lock约束；即使此时HEAD切走，操作也不会转而提交到新分支。
- 此批不模拟Git钩子/签名生命周期：发现可执行hooks或commit.gpgSign时禁止网页提交并说明需使用原Git流程；不静默跳过它们。detached HEAD不在网页提交范围，仍可读评审和暂存。
- 所有写入先审计并以request_id持久认领。另存操作日志，提交新OID/index目标hash在最终替换前持久化；响应未知通过操作查询核对实际ref/index，不重复提交或盲目重放。用户其他程序仍可改工作文件，版本检查不宣传为整仓文件系统事务。

验证只在隔离真实Git夹具中进行：暂存范围、取消暂存保留文件、首次提交、暂存/未暂存叠加、外部版本变化、已有index.lock、钩子/过滤器拒绝、提交响应重查和同请求去重。

真实Git 2.53探针发现 symref-verify HEAD 与更新其分支不能放入同一事务（Git为分支更新隐式加入HEAD日志更新，导致重复HEAD操作）；不能按文档语法推断该组合可用。改用上面的固定分支CAS契约，不声称HEAD与分支被一起原子核验，不再要求2.48。

原生 `git status` 也可能调用clean/process（真实夹具已复现）。状态读取改为私有Git元数据视图，指向原对象/引用/index，只复制内置格式选项，隔绝全局/系统和仓库外部命令配置。diff读取沿用相同机制；自定义filter文件不提供网页diff正文，仓库比较提示未执行外部转换、此类文件需另行检查，其他文件仍可评审。状态仍显示这些文件的受限说明。reftable等非files引用存储暂不支持此读取方式。
