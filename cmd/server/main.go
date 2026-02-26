package main

import "github.com/kitwork/engine"

func main() {
	// Khởi tạo Config với danh sách các tệp Database và SMTP cụ thể
	cfg := &engine.Config{
		Port:    8080,
		Sources: []string{"./public"},

		Master: []string{"config/database/master.yaml"}, // MasterDB
	}

	engine.Run(cfg)

	//engine.Test(`let a = 1; let b = 2; let c = a + b;`, 1000000)

	// fmt.Println("New Engine ID (Tenant):", id.Gen26())
	// fmt.Println("New Engine ID (Source):", id.Generate())
}
