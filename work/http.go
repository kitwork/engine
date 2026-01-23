package work

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

type HTTPClient struct {
	task *Task
}

func NewHTTPClient(t *Task) *HTTPClient {
	return &HTTPClient{task: t}
}

func (c *HTTPClient) Get(url string) value.Value {
	// Mock implementation
	fmt.Printf("[%s] HTTP GET -> %s\n", c.task.Work.Name, url)

	res := make(map[string]value.Value)
	res["status"] = value.New(200)
	res["body"] = value.New(fmt.Sprintf("Response from %s", url))
	return value.New(res)
}

func (c *HTTPClient) Post(url string, body value.Value) value.Value {
	// Mock implementation
	fmt.Printf("[%s] HTTP POST -> %s | Body: %s\n", c.task.Work.Name, url, body.Text())

	res := make(map[string]value.Value)
	res["status"] = value.New(201)
	res["id"] = value.New("req_12345")
	return value.New(res)
}
