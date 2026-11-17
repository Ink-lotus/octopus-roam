package db

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db/migrate"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

func debugLogger(writer logger.Writer) logger.Interface {
	return logger.New(writer, logger.Config{
		SlowThreshold: 200 * time.Millisecond,
		LogLevel:      logger.Info,
		Colorful:      true,
		// Retain statement structure/timing without adding bound parameters to logs.
		ParameterizedQueries: true,
	})
}

func InitDB(dbType, dsn string, debug bool) error {
	var err error
	gormConfig := gorm.Config{Logger: logger.Discard}
	if debug {
		gormConfig.Logger = debugLogger(log.New(os.Stdout, "\r\n", log.LstdFlags))
	}

	switch dbType {
	case "sqlite":
		db, err = initSQLite(dsn, &gormConfig)
	case "mysql":
		db, err = initMySQL(dsn, &gormConfig)
	case "postgres", "postgresql":
		db, err = initPostgres(dsn, &gormConfig)
	default:
		return fmt.Errorf("unsupported database type: %s", dbType)
	}

	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	configurePool(sqlDB)

	if err := migrate.BeforeAutoMigrate(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(
		&model.User{},
		&model.Channel{},
		&model.ChannelRefreshRecord{},
		&model.ChannelKey{},
		&model.ChannelModel{},
		&model.ChannelGrant{},
		&model.GroupCategory{},
		&model.Group{},
		&model.GroupItem{},
		&model.LLMInfo{},
		&model.APIKey{},
		&model.Setting{},
		&model.WebhookEndpoint{},
		&model.StatsTotal{},
		&model.StatsDaily{},
		&model.StatsHourly{},
		&model.StatsHourlyArchive{},
		&model.StatsAPIKey{},
		&migrate.MigrationRecord{},
	); err != nil {
		return err
	}
	if err := migrate.AfterAutoMigrate(db); err != nil {
		return err
	}
	// Migrations may invalidate prepared plans. SQL DEALLOCATE/DISCARD clears
	// only server state, leaving pgx's client cache referencing missing statements
	// (SQLSTATE 26000). A fresh pool clears both caches on every connection.
	if db.Dialector != nil && db.Dialector.Name() == "postgres" {
		refreshed, err := initPostgres(dsn, &gormConfig)
		if err != nil {
			return err
		}
		refreshedPool, err := refreshed.DB()
		if err != nil {
			return err
		}
		if err := sqlDB.Close(); err != nil {
			_ = refreshedPool.Close()
			return err
		}
		configurePool(refreshedPool)
		db = refreshed
	}
	return nil
}

func configurePool(pool *sql.DB) {
	pool.SetMaxIdleConns(10)
	pool.SetMaxOpenConns(100)
	pool.SetConnMaxLifetime(time.Hour)
	pool.SetConnMaxIdleTime(10 * time.Minute)
}

// initSQLite 使用指定文件路径初始化 SQLite，并为每个连接应用运行参数。
func initSQLite(path string, config *gorm.Config) (*gorm.DB, error) {
	params := url.Values{}
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "synchronous(NORMAL)")
	params.Add("_pragma", "cache_size(10000)")
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "foreign_keys(ON)")
	params.Add("_pragma", "auto_vacuum(INCREMENTAL)")
	params.Add("_pragma", "mmap_size(268435456)")
	params.Add("_pragma", "locking_mode(NORMAL)")
	return gorm.Open(sqlite.Open(path+"?"+params.Encode()), config)
}

func initMySQL(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
	if !strings.Contains(dsn, "?") {
		dsn += "?charset=utf8mb4&parseTime=True&loc=Local"
	}
	return gorm.Open(mysql.Open(dsn), config)
}

func initPostgres(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: host=localhost user=postgres password=xxx dbname=octopus-roam port=5432 sslmode=disable
	return gorm.Open(postgres.Open(dsn), config)
}

func Close() error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func GetDB() *gorm.DB {
	return db
}
