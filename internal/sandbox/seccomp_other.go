//go:build !linux

package sandbox

// 只有 Linux 有 seccomp（Run 在别的系统上直接报“沙箱只支持 Linux”），这里只为让包能编译。
func seccompProgram() []byte {
	return nil
}
