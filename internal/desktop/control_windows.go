package desktop

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Controller struct {
	Directory string
	Autostart *Autostart
}

func NewController(directory string, tasks TaskStore) (*Controller, error) {
	directory, err := Directory(directory)
	if err != nil {
		return nil, err
	}
	autostart, err := NewAutostart(directory, tasks)
	if err != nil {
		return nil, err
	}
	return &Controller{Directory: directory, Autostart: autostart}, nil
}

func (c *Controller) Start(enableAutostart bool) error {
	unlock, err := Lock(c.Directory, "", 10*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	if enableAutostart {
		state, err := c.Autostart.State()
		if err != nil {
			return err
		}
		if state == TaskConflict {
			return fmt.Errorf("此部署的计划任务已被修改，请先检查其目标和参数")
		}
	}
	if err := c.start(); err != nil {
		return err
	}
	if enableAutostart {
		return c.Autostart.Enable()
	}
	return nil
}

func (c *Controller) start() error {
	process, ready, err := Running(c.Directory)
	if err != nil {
		return err
	}
	if process != nil {
		defer process.Close()
		if ready {
			return nil
		}
		return fmt.Errorf("服务正在初始化，请稍后重试")
	}
	executable := filepath.Join(c.Directory, "octopus-roam.exe")
	others, err := Processes(executable)
	if err != nil {
		return err
	}
	unmanaged := false
	for _, other := range others {
		if other.PID != os.Getpid() {
			unmanaged = true
		}
		other.Close()
	}
	if unmanaged {
		return fmt.Errorf("本目录存在旧版或未托管的进程，请先在原终端正常关闭")
	}
	if err := prepare(c.Directory); err != nil {
		return err
	}
	host, port, err := Endpoint(c.Directory)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("监听端口不可用，请检查 data/config.json 和端口占用")
	}
	listener.Close()
	out, err := os.CreateTemp(filepath.Join(c.Directory, "logs"), "startup-"+time.Now().Format("20060102-150405")+"-*.out.log")
	if err != nil {
		return err
	}
	defer out.Close()
	stderr, err := os.OpenFile(out.Name()+".err.log", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer stderr.Close()
	command := exec.Command(executable, "start", "--config", filepath.Join("data", "config.json"))
	command.Dir, command.Stdout, command.Stderr = c.Directory, out, stderr
	Hidden(command)
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return fmt.Errorf("服务在启动时退出，请检查 logs 文件夹")
		case <-timer.C:
			return fmt.Errorf("服务启动尚未完成，请检查 logs 后重试")
		case <-tick.C:
			process, ready, err := Running(c.Directory)
			if err != nil {
				return err
			}
			if process != nil {
				matches := process.PID == command.Process.Pid
				process.Close()
				if matches && ready {
					return nil
				}
			}
		}
	}
}

func (c *Controller) Stop(disableAutostart bool) error {
	unlock, err := Lock(c.Directory, "", 10*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	if disableAutostart {
		if err := c.Autostart.Disable(); err != nil {
			return err
		}
	}
	return RequestStop(c.Directory, 60*time.Second)
}
