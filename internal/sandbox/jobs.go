package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/xuzhao123/tern/internal/llm"
	"github.com/xuzhao123/tern/internal/protocol"
)

// 后台命令。bash 带 background=true 时不等它结束：在同样的沙箱里启动（同样的资源限制、断网与代理），
// 立刻返回 job_id；之后用 bash_output 读新输出、看结束没有，用 bash_input 回答它的提示，用 bash_kill 停掉。
// 适合监视文件、跑很久的构建、要回答提示的安装脚本。
// 每条命令都在自己的沙箱里（自己的进程与网络），后台起的服务，之后的命令看不到也连不上；同 Claude Code 在 Linux 上的沙箱，
// 启动结果照它的说法告诉模型：要测服务，在同一条命令里起服务再访问；查看和停止只经 bash_output、bash_kill。
// 后台命令只活到这一轮结束：进程退出前 Close 把还在跑的全部按 cgroup（macOS 上是进程组）杀掉，和前台命令被取消时一样，沙箱里不留进程。
// 标准输入留着写端（前台命令是 /dev/null）：bash_input 往里写；命令结束、被停掉或 close=true 时关掉写端，命令读到 EOF。
const (
	maxJobs       = 4       // 同时在跑的后台命令最多几个
	jobOutputMax  = 1 << 20 // 每个后台命令最多留 1MB 输出，超出时丢掉最早的
	maxJobWait    = 20 * time.Second
	jobPeekMax    = 8 << 10     // 给网页端看时只取最近的 8KB
	jobCommandMax = 200         // job/updated 里带多少字的命令原文
	jobInputMax   = 64 << 10    // bash_input 一次最多写 64KB：命令没在读的时候，多写的只会堆在管道里
	jobInputWait  = time.Second // bash_input 写完之后等这么久的新输出，省一次 bash_output
)

type job struct {
	id      string
	command string // 命令原文（最多 jobCommandMax 个字）：界面靠它认出是哪条
	cmd     *exec.Cmd
	unit    string
	started time.Time
	done    chan struct{} // 进程结束后关闭

	mu      sync.Mutex
	stdin   *os.File // 标准输入的写端：bash_input 往里写；关掉之后是 nil
	buf     []byte   // 还留着的输出
	dropped int      // 超过上限被丢掉的字节数（从最早的开始）
	read    int      // 已经交给模型的位置：按全部输出算的偏移
	exit    int
	ended   time.Time // 进程结束的时间
	stopped string    // 不是自己结束的：被 bash_kill 停掉，或本轮结束时被终止
	state   string    // 已经发出去的 job/updated 状态：同一个状态不重复发
}

func (j *job) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buf = append(j.buf, p...)
	if extra := len(j.buf) - jobOutputMax; extra > 0 {
		j.buf, j.dropped = j.buf[extra:], j.dropped+extra
	}
	return len(p), nil
}

var (
	jobsMu sync.Mutex
	jobs   = map[string]*job{}
	jobSeq int
)

func startJob(ctx context.Context, callID, command string, tty bool) (any, error) {
	if tty {
		// script 要在沙箱里跑：宿主机上找不到它，沙箱里（只读挂的 /usr）也没有。
		if _, err := exec.LookPath("script"); err != nil {
			return nil, llm.Permanent(errors.New("沙箱里没有 script，不能开伪终端；去掉 tty 再跑"))
		}
	}
	jobsMu.Lock()
	running := 0
	for _, j := range jobs {
		select {
		case <-j.done:
		default:
			running++
		}
	}
	if running >= maxJobs {
		jobsMu.Unlock()
		return nil, llm.Permanent(fmt.Errorf("最多同时 %d 个后台命令；先用 bash_kill 停掉不用的", maxJobs))
	}
	jobSeq++
	j := &job{id: fmt.Sprintf("job-%d", jobSeq), command: jobCommand(command), started: time.Now(), done: make(chan struct{})}
	jobsMu.Unlock()
	// 标准输入是管道：读端交给沙箱，写端留着给 bash_input（子进程拿到自己的副本，这边马上关掉读端）。
	r, w, err := os.Pipe()
	if err != nil {
		return nil, llm.Permanent(err)
	}
	j.stdin = w
	// 和前台命令一样在锁里启动（共用工作目录与代理的初始化），启动完就放锁，不占着它等命令结束。
	mu.Lock()
	cmd, unit, err := launch(ctx, callID, command, j, r, tty, ProjectWrite)
	mu.Unlock()
	r.Close()
	if err != nil {
		w.Close()
		return nil, err
	}
	j.cmd, j.unit = cmd, unit
	jobsMu.Lock()
	jobs[j.id] = j
	jobsMu.Unlock()
	j.jobUpdated()
	go func() {
		cmd.Wait()
		j.mu.Lock()
		j.exit, j.ended = cmd.ProcessState.ExitCode(), time.Now()
		j.mu.Unlock()
		// 命令结束了，标准输入的写端也关掉：不等它自己读，也不要留着这个 fd。
		j.closeInput()
		close(j.done)
		j.jobUpdated()
		fmt.Printf("Bash job [%s]: exited exit=%d after=%s\n", j.id, cmd.ProcessState.ExitCode(), time.Since(j.started).Round(time.Millisecond))
	}()
	fmt.Printf("Bash job [%s]: started call=%s\n", j.id, callID)
	return map[string]any{"job_id": j.id, "status": "running",
		"note": "命令在后台它自己的沙箱里运行：之后的命令看不到、也连不上它和它起的服务，所以不要用 ps、pgrep、pkill 或网络请求查它还在不在。" +
			"用 bash_output 读输出、看结束没有（可以带 wait 等几秒），要回答它的提示用 bash_input 写输入，用 bash_kill 停掉；这一轮结束时还在跑的会被终止。要测一个服务，在同一条命令里起服务再访问。"}, nil
}

func findJob(id string) (*job, error) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	if j := jobs[strings.TrimSpace(id)]; j != nil {
		return j, nil
	}
	return nil, llm.Permanent(errors.New("没有这个后台命令：job_id 来自 bash 带 background=true 的返回；后台命令只在本轮内有效"))
}

// jobOutput 交回上次读到之后的新输出；wait 秒内命令结束了就提前返回。等待不超过 maxJobWait 和单条命令时限的一半。
func jobOutput(id string, wait int) (any, error) {
	j, err := findJob(id)
	if err != nil {
		return nil, err
	}
	if wait > 0 {
		limit := min(time.Duration(wait)*time.Second, maxJobWait, Timeout/2)
		select {
		case <-j.done:
		case <-time.After(limit):
		}
	}
	return j.snapshot(), nil
}

// jobKill 停掉一个后台命令（按 cgroup 整体结束），交回剩下的输出。已经结束的直接交回。
func jobKill(id string) (any, error) {
	j, err := findJob(id)
	if err != nil {
		return nil, err
	}
	j.stop("被 bash_kill 停掉")
	return j.snapshot(), nil
}

// jobInput 往后台命令的标准输入原样写 text（要回车就自己带 \n），close 时写完关掉（命令读到 EOF）。
// 交回写之后 1 秒内的新输出，格式和 bash_output 一样：回答提示之后往往立刻就有反应，省一次 bash_output。
func jobInput(id, text string, closeStdin bool) (any, error) {
	j, err := findJob(id)
	if err != nil {
		return nil, err
	}
	if text == "" && !closeStdin {
		return nil, llm.Permanent(errors.New("text 和 close 至少要有一个：text 是要写进去的字（要回车就自己带 \\n），close=true 关掉标准输入"))
	}
	if len(text) > jobInputMax {
		return nil, llm.Permanent(fmt.Errorf("一次最多写 %dKB，分几次写", jobInputMax>>10))
	}
	select {
	case <-j.done:
		return nil, llm.Permanent(errors.New("这个后台命令已经结束，不能再写输入；用 bash_output 读它留下的输出"))
	default:
	}
	note := ""
	if text != "" {
		pending, err := j.writeInput(text)
		if err != nil {
			return nil, err
		}
		if pending {
			note = "输入还没写完：命令没有在读标准输入，等它读的时候才到（也可能它永远不会读）"
		}
	}
	if closeStdin {
		j.closeInput()
	}
	select {
	case <-j.done:
	case <-time.After(jobInputWait):
	}
	result := j.snapshot()
	if note != "" {
		result["note"] = note
	}
	return result, nil
}

// writeInput 把 text 写进标准输入。命令没在读的时候写会停在管道的缓冲里，所以放到另一处写、最多等 jobInputWait：
// 一直等下去的话，这条工具调用会卡在这里，而命令可能永远不读。返回是否还没写完。
func (j *job) writeInput(text string) (bool, error) {
	j.mu.Lock()
	w := j.stdin
	j.mu.Unlock()
	if w == nil {
		return false, llm.Permanent(errors.New("这个后台命令的标准输入已经关掉了（写过 close=true，或者命令已经结束）"))
	}
	errs := make(chan error, 1)
	go func() {
		_, err := io.WriteString(w, text)
		errs <- err
	}()
	select {
	case err := <-errs:
		if err != nil {
			return false, llm.Permanent(fmt.Errorf("写不进这个后台命令的标准输入：%w", err))
		}
		return false, nil
	case <-time.After(jobInputWait):
		return true, nil
	}
}

// closeInput 关掉标准输入的写端（发 EOF）。命令结束、被停掉时也走这里，所以重复调用什么也不做。
func (j *job) closeInput() {
	j.mu.Lock()
	w := j.stdin
	j.stdin = nil
	j.mu.Unlock()
	if w != nil {
		w.Close()
	}
}

func (j *job) stop(why string) {
	select {
	case <-j.done:
		return
	default:
	}
	j.mu.Lock()
	j.stopped = why
	j.mu.Unlock()
	kill(j.cmd, j.unit)
	<-j.done
	j.jobUpdated()
}

// jobUpdated 发一条 job/updated：后台命令启动、自己结束、被停掉时各发一次（见 internal/protocol）。
// 网页端据此列出这一轮的后台命令、刷新顶栏的数目；同一个状态只发一次：被停掉时 stop 和等进程结束的那一段都会走到这里。
func (j *job) jobUpdated() {
	j.mu.Lock()
	state := "running"
	p := map[string]any{"id": j.id, "command": j.command, "started_at": j.started}
	select {
	case <-j.done:
		state, p["exit_code"] = "exited", j.exit
		if !j.ended.IsZero() {
			p["ended_at"] = j.ended
		}
		if j.stopped != "" {
			state, p["stopped"] = "stopped", j.stopped
		}
	default:
	}
	if j.state == state {
		j.mu.Unlock()
		return
	}
	j.state, p["state"] = state, state
	j.mu.Unlock()
	protocol.Notify("job/updated", p)
}

// jobCommand 只留前 jobCommandMax 个字：命令原文可能很长，界面只用它认出是哪条。
func jobCommand(command string) string {
	r := []rune(strings.TrimSpace(command))
	if len(r) <= jobCommandMax {
		return string(r)
	}
	return string(r[:jobCommandMax-1]) + "…"
}

// jobPeek 交回最近的 jobPeekMax 字节输出，只看不消费：不动 j.read，模型下次 bash_output 照样从上次的位置读到。
// dropped 是没显示出来的字节数（超过 1MB 被丢掉的，加上更早、这里没取的那部分）。
func jobPeek(id string) (any, error) {
	j, err := findJob(id)
	if err != nil {
		return nil, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	out, dropped := j.buf, j.dropped
	if extra := len(out) - jobPeekMax; extra > 0 {
		out, dropped = out[extra:], dropped+extra
	}
	state := "running"
	select {
	case <-j.done:
		state = "exited"
		if j.stopped != "" {
			state = "stopped"
		}
	default:
	}
	return map[string]any{"output": strings.ToValidUTF8(string(out), "�"), "dropped": dropped, "state": state}, nil
}

// JobOutput 是 job/output 用的入口：网页端看一个后台命令最近的输出。
func JobOutput(id string) (any, error) {
	return jobPeek(id)
}

// JobStop 是 job/kill 用的入口：网页端停掉一个后台命令（和 bash_kill 一样按 cgroup 结束），
// stopped 写“用户在网页上停掉”。交回的输出也不消费，模型下次 bash_output 照样从上次的位置读到。
func JobStop(id string) (any, error) {
	j, err := findJob(id)
	if err != nil {
		return nil, err
	}
	j.stop("用户在网页上停掉")
	return jobPeek(id)
}

func (j *job) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	from, note := j.read-j.dropped, ""
	if from < 0 {
		note = fmt.Sprintf("有 %d 字节输出在读到之前就超过了 1MB 的上限，被丢掉了", -from)
		from = 0
	}
	out := j.buf[from:]
	j.read = j.dropped + len(j.buf)
	r := map[string]any{"job_id": j.id, "output": strings.ToValidUTF8(string(out), "�"), "output_bytes": len(out), "elapsed_ms": time.Since(j.started).Milliseconds()}
	select {
	case <-j.done:
		r["status"], r["exit_code"] = "exited", j.exit
		if j.stopped != "" {
			r["stopped"] = j.stopped
		}
	default:
		r["status"] = "running"
	}
	if note != "" {
		r["note"] = note
	}
	return r
}

// stopJobs 在进程退出前终止所有还在跑的后台命令（Close 调用）。
func stopJobs() {
	jobsMu.Lock()
	list := make([]*job, 0, len(jobs))
	for _, j := range jobs {
		list = append(list, j)
	}
	jobsMu.Unlock()
	for _, j := range list {
		j.stop("本轮结束时被终止")
	}
}
