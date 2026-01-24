package work

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/kitwork/engine/security"
	_ "github.com/lib/pq"
)

var globalDB *sql.DB

// InitDB khởi tạo kết nối Database toàn cục dựa trên cấu hình bảo mật
func InitDB(cfg security.DBConfig) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s timezone=%s connect_timeout=%d",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode, cfg.Timezone, cfg.Timeout)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}

	db.SetMaxOpenConns(cfg.MaxOpen)
	db.SetMaxIdleConns(cfg.MaxIdle)
	db.SetConnMaxLifetime(time.Duration(cfg.Lifetime) * time.Minute)

	if err := db.Ping(); err != nil {
		return err
	}

	globalDB = db
	return nil
}

func GetDB() *sql.DB {
	return globalDB
}
