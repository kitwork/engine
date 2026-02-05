package main

import "github.com/kitwork/engine"

func main() {
	// Khởi tạo Config với danh sách các tệp Database và SMTP cụ thể
	cfg := &engine.Config{
		Port:    8081,
		Debug:   false,
		Sources: []string{"./demo"},
		Assets: []engine.Asset{
			{Dir: "./demo/public", Path: "/public"},
		},
		Databases: []string{"config/database/master.yaml"},
		SMTPS:     []string{"config/smtp/mail.yaml"}, // Nạp cấu hình SMTP từ file
	}

	engine.Run(cfg)

	//engine.Test(`let a = 1; let b = 2; let c = a + b;`, 1000000)
}
