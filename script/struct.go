package script

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

type Script struct {
	timeout time.Duration
}

func New() *Script {
	return &Script{
		timeout: 6 * time.Second, // 6s is a good sweet spot for web requests
	}
}

func Run(source string) (value.Value, error) {
	return New().Run(source)
}

func (s *Script) Timeout(timeout time.Duration) *Script {
	s.timeout = timeout
	return s
}

func (s *Script) code(source string) (content string, err error) {
	if strings.HasSuffix(source, ".js") {
		if _, err := os.Stat(source); err == nil {
			content, _ := os.ReadFile(source)
			return string(content), nil
		}
		return "", fmt.Errorf("file not found: %s", source)
	}
	return source, nil
}

func (s *Script) Run(source string) (value.Value, error) {
	code, err := s.code(source)
	if err != nil {
		return value.Value{K: value.Invalid}, err
	}

	l := compiler.NewLexer(code)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return value.Value{K: value.Invalid}, fmt.Errorf("compile error: %s", p.Errors()[0])
	}

	stdlib := compiler.NewEnvironment()

	// ⏳ Tính năng chống treo hệ thống (Timeout Handling)
	if s.timeout > 0 {
		// Tạo channel để nhận kết quả từ goroutine thực thi
		done := make(chan value.Value, 1) // Buffer 1 để tránh goroutine rò rỉ nếu bị timeout
		errChan := make(chan error, 1)

		go func() {
			defer func() {
				// Bắt lỗi Panic nếu có trong lúc chạy script
				if r := recover(); r != nil {
					errChan <- fmt.Errorf("panic inside script evaluator: %v", r)
				}
			}()

			res := compiler.Evaluator(prog, stdlib)
			if res.IsInvalid() {
				errChan <- fmt.Errorf("runtime error during Evaluation")
			} else {
				done <- res
			}
		}()

		// Dùng Select để "đua" giữa kênh Trả-về và kênh Chờ-giờ
		select {
		case res := <-done:
			return res, nil
		case evalErr := <-errChan:
			return value.Value{K: value.Invalid}, evalErr
		case <-time.After(s.timeout):
			return value.Value{K: value.Invalid}, fmt.Errorf("script execution timed out after %v", s.timeout)
		}
	}

	// Chạy bình thường nếu không set Timeout
	res := compiler.Evaluator(prog, stdlib)
	if res.IsInvalid() {
		return value.Value{K: value.Invalid}, fmt.Errorf("runtime error during Evaluation")
	}
	return res, nil
}
