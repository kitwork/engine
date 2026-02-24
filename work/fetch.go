package work

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/kitwork/engine/value"
)

type Fetch struct {
	task *Task
}

func NewFetch(t *Task) *Fetch {
	return &Fetch{task: t}
}

func (c *Fetch) Get(url string) value.Value {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	res := make(map[string]value.Value)
	res["status"] = value.New(resp.StatusCode)

	// Try to parse as JSON if possible
	var jsonData any
	if err := json.Unmarshal(body, &jsonData); err == nil {
		res["body"] = value.New(jsonData)
	} else {
		res["body"] = value.New(string(body))
	}

	return value.New(res)
}

func (c *Fetch) Post(url string, bodyVal value.Value) value.Value {
	client := &http.Client{Timeout: 10 * time.Second}

	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(bodyVal.Interface())

	resp, err := client.Post(url, "application/json", &buf)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	defer resp.Body.Close()

	resBody, _ := io.ReadAll(resp.Body)

	res := make(map[string]value.Value)
	res["status"] = value.New(resp.StatusCode)

	var jsonData any
	if err := json.Unmarshal(resBody, &jsonData); err == nil {
		res["body"] = value.New(jsonData)
	} else {
		res["body"] = value.New(string(resBody))
	}

	return value.New(res)
}
