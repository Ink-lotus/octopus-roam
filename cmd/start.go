package cmd

import (
	"fmt"
	"github.com/Ink-lotus/octopus-roam/internal/conf"
	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/desktop"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/server"
	"github.com/Ink-lotus/octopus-roam/internal/task"
	"github.com/Ink-lotus/octopus-roam/internal/utils/shutdown"
	"github.com/Ink-lotus/octopus-roam/internal/webhook"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

var cfgFile string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start " + conf.APP_NAME,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		conf.PrintBanner()
		if err := conf.Load(cfgFile); err != nil {
			return err
		}
		if level, err := log.ParseLevel(conf.AppConfig.Log.Level); err == nil {
			log.SetLevel(level)
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		session, err := desktop.BeginServer()
		if err != nil {
			return err
		}
		defer session.Close()
		shutdown.Init(log.Default())
		defer shutdown.Shutdown()
		databasePath, err := conf.AppConfig.Database.ConnectionPath()
		if err != nil {
			return fmt.Errorf("database configuration error")
		}
		if err := db.InitDB(conf.AppConfig.Database.Type, databasePath, conf.IsDebug()); err != nil {
			log.Errorf("database init error: %v", err)
			return fmt.Errorf("database initialization failed")
		}
		shutdown.Register(db.Close)

		if err := op.InitCache(); err != nil {
			log.Errorf("cache init error: %v", err)
			return fmt.Errorf("cache initialization failed")
		}
		shutdown.Register(op.SaveCache)

		webhookService := webhook.Start()
		shutdown.Register(func() error {
			webhookService.Close()
			return nil
		})

		if err := op.UserInit(); err != nil {
			log.Errorf("user init error: %v", err)
			return fmt.Errorf("user initialization failed")
		}

		if err := server.Start(); err != nil {
			log.Errorf("server start error: %v", err)
			return fmt.Errorf("server initialization failed")
		}
		shutdown.Register(server.Close)

		task.Init()
		go task.RUN()
		if err := session.Ready(); err != nil {
			return err
		}
		shutdown.ListenFor(session.Stop())
		return nil
	},
}

func init() {
	startCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./data/config.json)")
	rootCmd.AddCommand(startCmd)
}
