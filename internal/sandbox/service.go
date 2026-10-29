package sandbox

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 仅专用服务worker使用；普通bash与后台命令不导出端口。
var ServicePorts string
var ServiceLog bool

func serviceExports() error {
	ports := os.Getenv("TERN_SERVICE_PORTS")
	if ports == "" {
		return nil
	}
	dir := filepath.Join(os.Getenv("HOME"), ".tern-ports")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for _, value := range strings.Split(ports, ",") {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1024 || port > 65535 || value == proxyPort {
			return fmt.Errorf("服务端口无效")
		}
		path := filepath.Join(dir, value+".sock")
		listener, err := net.Listen("unix", path)
		if err != nil {
			return err
		}
		os.Chmod(path, 0600)
		go func() {
			defer listener.Close()
			for {
				client, err := listener.Accept()
				if err != nil {
					return
				}
				go func() {
					upstream, err := net.Dial("tcp", "127.0.0.1:"+value)
					if err != nil {
						client.Close()
						return
					}
					go pipe(upstream, client)
					pipe(client, upstream)
				}()
			}
		}()
	}
	return nil
}
