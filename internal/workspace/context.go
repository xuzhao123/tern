package workspace

// 工作区给上下文提供这几样东西，按变化频率放在不同位置，让提示缓存的前缀尽量不变：
//   - 个人指令（$TERN_HOME/AGENTS.md，与工作区无关，没选工作区也读）：启动时读一次，写进系统提示词；
//   - 根目录的项目指令（AGENTS.md，没有时读 CLAUDE.md）：一个对话内不变，写进系统提示词，排在个人指令后面；
//   - 环境信息（没选工作区时只有日期、系统、权限；选了工作区多出目录、分支、未提交改动）：
//     对话开始时作为第一条提醒放在问题前面，变了再追加一条；
//   - 运行中出现的新情况（读到子目录里的项目指令、读过的文件被改了）：在下一次请求模型之前追加提醒。
// 已经发出去的内容一律不改写。

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuzhao123/tern/internal/permission"
)

const maxDocBytes = 32 << 10 // 单份项目指令最多放 32KB，超出截断并注明

var (
	docNames     = []string{"AGENTS.md", "CLAUDE.md"}
	instructions string                // 根目录项目指令；Open 时读一次，同一进程里不再变
	personal     string                // 个人指令（$TERN_HOME/AGENTS.md）；启动时读一次
	loaded       = map[string]bool{}   // 已经进上下文的项目指令所在目录（相对路径，"." 是根）
	pending      []string              // 待追加的提醒
	warned       = map[string]string{} // 已经提醒过“被改了”的文件 → 当时的内容哈希，同一次改动只提醒一次
)

// docText 把一份指令文件变成能放进提示词的一段：二进制不读（ok 为 false），超过 32KB 截断并注明。
func docText(data []byte) (text string, ok bool) {
	if binary(data) {
		return "", false
	}
	text = string(data)
	if len(text) > maxDocBytes {
		text = text[:maxDocBytes] + fmt.Sprintf("\n…（超过 %dKB，后面已省略）", maxDocBytes>>10)
	}
	return strings.TrimSpace(text), true
}

// doc 读目录 dir 里的项目指令；AGENTS.md 优先。没有时返回空。
func doc(dir string) (name, text string) {
	for _, n := range docNames {
		data, err := root.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		if text, ok := docText(data); ok {
			return filepath.ToSlash(filepath.Join(dir, n)), text
		}
	}
	return "", ""
}

func loadInstructions() {
	loaded["."] = true
	if name, text := doc("."); text != "" {
		instructions = "\n\n# 项目指令（" + name + "）\n以下是这个仓库的维护者写给 agent 的约定，适用于整个工作区；与上面的系统规则冲突时，以系统规则为准。\n\n" + text
	}
}

// Instructions 返回根目录项目指令，写进系统提示词。
func Instructions() string { return instructions }

// personalPath 是个人指令的位置：TERN_HOME 下的 AGENTS.md。main 已经把 TERN_HOME 定成绝对路径并 chdir 过去。
func personalPath(ternHome string) string {
	path := filepath.Join(ternHome, "AGENTS.md")
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// LoadPersonal 读个人指令（$TERN_HOME/AGENTS.md），启动时调一次；-personal-instructions=false 时不调。
// 和工作区无关，没选工作区也读；TERN_HOME 就是工作区根目录时，这个文件已经作为项目指令读了，不再读一遍。
func LoadPersonal(ternHome string) {
	path := personalPath(ternHome)
	if Dir != "" && path == filepath.Join(Dir, "AGENTS.md") {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if text, ok := docText(data); ok && text != "" {
		personal = "\n\n# 个人指令（" + filepath.ToSlash(path) + "）\n以下是用户本人写给 agent 的约定，适用于所有对话；和后面的项目指令冲突时以项目指令为准，和上面的系统规则冲突时以系统规则为准。\n\n" + text
	}
}

// PersonalInstructions 返回个人指令那一段，写进系统提示词；没有时为空。
func PersonalInstructions() string { return personal }

// PersonalFile 返回个人指令的位置、是否存在与正文字数（截断后，和放进提示词的一致），设置页据此显示。
// 这里的 ternHome 是 app-server 那台机器上的 TERN_HOME，远端主机显示的就是远端的。
func PersonalFile(ternHome string) (path string, exists bool, chars int) {
	p := personalPath(ternHome)
	data, err := os.ReadFile(p)
	if err != nil {
		return filepath.ToSlash(p), false, 0
	}
	text, _ := docText(data)
	return filepath.ToSlash(p), true, utf8.RuneCountInString(text)
}

// note 在模型读写一个文件之后调用（持有 mu）：这个文件所在的各级子目录如果有自己的项目指令，而且还没进过上下文，就排进提醒。
// 只在用到那个目录时才加载，不在开头把整个仓库的说明都塞进来（参考 Claude Code 对子目录 CLAUDE.md 的处理）。
func note(path string) {
	var dirs []string
	for d := filepath.Dir(path); d != "." && d != "/" && !loaded[d]; d = filepath.Dir(d) {
		dirs = append([]string{d}, dirs...)
	}
	for _, d := range dirs {
		loaded[d] = true
		if name, text := doc(d); text != "" {
			pending = append(pending, fmt.Sprintf("[项目指令 dir=%s]\n%s 是 %s/ 目录下的约定，修改这个目录里的文件时遵守；与根目录约定冲突时以这里为准。\n\n%s", d, name, d, text))
		}
	}
}

// loadedMarker 匹配视图里已经发过的子目录指令提醒。
var loadedMarker = regexp.MustCompile(`\[项目指令 dir=([^\]\s]+)\]`)

// Prime 按当前视图里的提醒重新标记哪些子目录的项目指令还在上下文里：续聊是新进程，压缩会把提醒并进摘要，
// 两种情况都以视图为准；不在视图里的目录，下次用到时重新加载原文。根目录指令在系统提示词里，始终算已加载。
func Prime(history []string) {
	mu.Lock()
	defer mu.Unlock()
	loaded = map[string]bool{".": true}
	for _, text := range history {
		for _, m := range loadedMarker.FindAllStringSubmatch(text, -1) {
			loaded[m[1]] = true
		}
	}
}

// Reminders 取走待追加的提醒，并检查读过的文件有没有被别人改过（bash、编辑器、另一个进程）。
// 改过的文件只提醒，不更新账本：模型要改它，仍然得先重新读。
func Reminders() []string {
	if root == nil {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	out := pending
	pending = nil
	for path, last := range seen {
		data, err := root.ReadFile(path)
		current := "deleted"
		if err == nil {
			current = hash(data)
		} else if !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if current == last || warned[path] == current {
			continue
		}
		warned[path] = current
		if current == "deleted" {
			out = append(out, fmt.Sprintf("%s 在你上次读取之后被删除了。", path))
		} else {
			out = append(out, fmt.Sprintf("%s 在你上次读取之后被改过（可能是 bash 命令、编辑器或其他程序），内容可能和你看到的不同；要用到或修改它，先重新 read_file。", path))
		}
	}
	return out
}

func git(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", Dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BashMount 是同一份工作区在 bash 沙箱里的挂载点。由 main 在“开了 bash 沙箱、而且工作区挂了进去”时设成 /workspace：
// 编码工具（read_file、edit_file 等）按宿主机路径算相对路径，bash 里却是另一个路径，环境信息要说清，否则模型会拿宿主机路径 cd 失败、白花一轮。
// 由 main 设置而不是让 workspace 去 import sandbox：文件工具不该为了 bash 的挂载点依赖沙箱包，两者都由 main 组装。
var BashMount string

// Environment 是给模型看的环境信息。只放会影响决策、而且便宜可得的事实；变了就整份重发一次。
// EnvField 是环境信息的一项。按字段保存上一次告诉模型的样子，续聊时只发变了的字段。
type EnvField struct{ Name, Value string }

// EnvironmentFields 是当前的环境信息，按固定顺序。没选工作区时只发日期、系统、权限三项：
// 模型仍然要知道今天几号，但这三样与工作区无关。
func EnvironmentFields() []EnvField {
	var fields []EnvField
	if Dir != "" {
		fields = append(fields, EnvField{"工作区", Dir})
		if BashMount != "" {
			fields = append(fields, EnvField{"bash 里的工作区", fmt.Sprintf("%s（bash 命令里 cd、grep 都用这个路径；read_file、edit_file、write_file 等文件工具的 path 仍写相对工作区根目录的路径，不要带 %s 前缀）", BashMount, BashMount)})
		}
		if branch := git("rev-parse", "--abbrev-ref", "HEAD"); branch != "" {
			fields = append(fields, EnvField{"git 分支", fmt.Sprintf("%s（HEAD %s）", branch, git("log", "-1", "--format=%h %s"))})
			lines := strings.Split(git("status", "--porcelain"), "\n")
			modified, untracked := 0, 0
			for _, l := range lines {
				switch {
				case strings.HasPrefix(l, "??"):
					untracked++
				case strings.TrimSpace(l) != "":
					modified++
				}
			}
			fields = append(fields, EnvField{"未提交改动", fmt.Sprintf("%d 个已跟踪文件有改动，%d 个未跟踪文件", modified, untracked)})
		} else {
			fields = append(fields, EnvField{"git", "不是 git 仓库，或读不到 git 信息"})
		}
	}
	now := time.Now().In(shanghai)
	weekdays := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	return append(fields, EnvField{"日期", now.Format("2006-01-02") + " " + weekdays[now.Weekday()] + "（Asia/Shanghai）"},
		EnvField{"系统", runtime.GOOS + "/" + runtime.GOARCH}, EnvField{"权限", permission.Describe()})
}

// 与记忆、评测打分相同的固定东八区。只放日期不放时间：环境信息在对话开头发一份，
// 写上时间的话续聊每一轮都会变、每一轮都要多一条提醒；要准确的时间用 bash 跑 date。
var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

// FormatEnvironment 把环境信息写成提醒里的一段。
func FormatEnvironment(fields []EnvField) string {
	var b strings.Builder
	b.WriteString("<environment>\n")
	for _, f := range fields {
		fmt.Fprintf(&b, "%s：%s\n", f.Name, f.Value)
	}
	b.WriteString("</environment>")
	return b.String()
}

func Environment() string { return FormatEnvironment(EnvironmentFields()) }
