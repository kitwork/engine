package work

import (
	"database/sql"
	"fmt"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/kitwork/engine/security"
	"github.com/kitwork/engine/value"
	_ "github.com/lib/pq"
)

var globalDB *sql.DB
var dbRegistry = make(map[string]*sql.DB)
var globalCache *lru.Cache[string, CacheItem]

type CacheItem struct {
	Value     value.Value
	ExpiresAt time.Time
}

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

	// Init Cache with default size 1000
	cache, err := lru.New[string, CacheItem](1000)
	if err != nil {
		return err
	}
	globalCache = cache

	return nil
}

func GetDB(name ...string) *sql.DB {
	if len(name) > 0 && name[0] != "" {
		if db, ok := dbRegistry[name[0]]; ok {
			return db
		}
	}
	return globalDB
}

func InitNamedDB(name string, cfg security.DBConfig) error {
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

	dbRegistry[name] = db
	return nil
}

func GetCache(key string) (value.Value, bool) {
	if globalCache == nil {
		return value.Value{K: value.Nil}, false
	}

	item, ok := globalCache.Get(key)
	if !ok {
		return value.Value{K: value.Nil}, false
	}

	if time.Now().After(item.ExpiresAt) {
		globalCache.Remove(key)
		return value.Value{K: value.Nil}, false
	}

	return item.Value, true
}

func SetCache(key string, val value.Value, ttl time.Duration) {
	if globalCache == nil {
		return
	}
	globalCache.Add(key, CacheItem{
		Value:     val,
		ExpiresAt: time.Now().Add(ttl),
	})
}
