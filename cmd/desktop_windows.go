package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/desktop"
	"github.com/Ink-lotus/octopus-roam/internal/update"
	"github.com/spf13/cobra"
)

func init() {
	var directory string
	parent := &cobra.Command{Use: "desktop", Short: "Manage the local Windows deployment"}
	parent.PersistentFlags().StringVar(&directory, "runtime", "", "Deployment directory (defaults to the executable directory)")
	controller := func() (*desktop.Controller, error) {
		if directory == "" {
			executable, err := os.Executable()
			if err != nil {
				return nil, err
			}
			directory = filepath.Dir(executable)
		}
		return desktop.NewController(directory, desktop.Scheduler{})
	}
	var enable, disable bool
	start := &cobra.Command{Use: "start", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, err := controller()
		if err != nil {
			return err
		}
		if err := c.Start(enable); err != nil {
			return err
		}
		url, err := desktop.AccessURL(c.Directory)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "服务已启动，管理页面："+url)
		return nil
	}}
	start.Flags().BoolVar(&enable, "autostart", false, "Also enable the current user's logon task")
	stop := &cobra.Command{Use: "stop", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, err := controller()
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "正在等待服务保存数据并退出……")
		if err := c.Stop(disable); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "服务已关闭。")
		return nil
	}}
	stop.Flags().BoolVar(&disable, "autostart", false, "Also remove this deployment's logon task")
	status := &cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, err := controller()
		if err != nil {
			return err
		}
		process, ready, err := desktop.Running(c.Directory)
		if err != nil {
			return err
		}
		service := "已停止"
		if process != nil {
			service = "启动中"
			if ready {
				service = "运行中"
			}
			process.Close()
		}
		state, err := c.Autostart.State()
		if err != nil {
			return err
		}
		labels := map[desktop.TaskState]string{desktop.TaskEnabled: "已开启", desktop.TaskDisabled: "已关闭", desktop.TaskConflict: "计划任务已被修改"}
		fmt.Fprintf(cmd.OutOrStdout(), "服务：%s    登录自启：%s\n", service, labels[state])
		return nil
	}}
	boot := &cobra.Command{Use: "boot", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		desktop.DetachConsole()
		c, err := controller()
		if err != nil {
			return err
		}
		if err := c.Start(false); err != nil {
			if os.MkdirAll(filepath.Join(c.Directory, "logs"), 0o755) == nil {
				if file, openErr := os.OpenFile(filepath.Join(c.Directory, "logs", "autostart-errors.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); openErr == nil {
					fmt.Fprintf(file, "%s %s\n", time.Now().Format(time.RFC3339), err)
					file.Close()
				}
			}
			return err
		}
		return nil
	}}
	parent.AddCommand(start, stop, status, boot)
	// A build-time updater can already hold the operation mutex. This command
	// sends only the same verified, cooperative shutdown request.
	parent.AddCommand(&cobra.Command{Use: "request-stop", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, err := controller()
		if err != nil {
			return err
		}
		return desktop.RequestStop(c.Directory, 60*time.Second)
	}})
	rootCmd.AddCommand(parent)
	rootCmd.AddCommand(&cobra.Command{Use: "__update-apply PLAN", Hidden: true, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return update.RunWindowsUpdateHelper(args[0]) }})
}
