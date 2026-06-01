package db

import (
	"fmt"

	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/api/comment"
	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/message"
	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/api/tag"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/config"
	"github.com/kiritosuki/GoVideo/internal/worker"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// NewDB 创建数据库连接 返回gorm.DB对象 用于数据库相关操作
func NewDB(dbConfig config.DatabaseConfig) (*gorm.DB, error) {
	// dsn = user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		dbConfig.User, dbConfig.Password, dbConfig.Host, dbConfig.Port, dbConfig.DBName)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// AutoMigrate 自动建表
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		// TODO 这里写数据结构
		&account.Account{},
		&video.Video{},
		&video.OutboxMsg{},
		&tag.Tag{},
		&tag.VideoTag{},
		&like.Like{},
		&comment.Comment{},
		&social.Social{},
		&message.Message{},
		&worker.Notification{},
	)
}

// CloseDB 关闭数据库连接
func CloseDB(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
