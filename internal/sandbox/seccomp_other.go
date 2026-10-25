//go:build !linux

package sandbox

// 只有 Linux 有 seccomp（macOS 上走 Seatbelt，不生成 BPF；其他系统直接报不可用），这里只为让包能编译。
func seccompProgram() []byte {
	return nil
}
