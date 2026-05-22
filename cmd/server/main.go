package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/kitwork/engine"
	"gopkg.in/yaml.v3"
)

func loadConfig() *engine.Config {
	// Khởi tạo Config với giá trị mặc định
	cfg := &engine.Config{
		Port:   8080,
		Source: "public",
	}

	// 1. Thử đọc file JSON trước
	jsonBytes, err := os.ReadFile("config.kitwork.json")
	if err == nil {
		if err := json.Unmarshal(jsonBytes, cfg); err != nil {
			fmt.Println("Lỗi parsing config.kitwork.json:", err)
		} else {
			fmt.Println("Đã nạp cấu hình từ config.kitwork.json")
			return cfg
		}
	}

	// 2. Thử đọc file YAML nếu không có JSON
	yamlBytes, err := os.ReadFile("config.kitwork.yaml")
	if err == nil {
		if err := yaml.Unmarshal(yamlBytes, cfg); err != nil {
			fmt.Println("Lỗi parsing config.kitwork.yaml:", err)
		} else {
			fmt.Println("Đã nạp cấu hình từ config.kitwork.yaml")
			return cfg
		}
	}

	// 3. Không có file nào thì dùng fallback default
	fmt.Println("Không tìm thấy file cấu hình (yaml/json). Sử dụng cấu hình mặc định.")

	// Default master db (giữ nguyên logic cũ của bạn nếu không cấu hình)
	if len(cfg.Master) == 0 {
		cfg.Master = []string{"config/database/master.yaml"}
	}

	return cfg
}

func main() {
	cfg := loadConfig()
	engine.Run(cfg)
}
