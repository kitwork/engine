package value

import "testing"

// callableHandle is a proxy a host object hands out through a getter, which is also callable:
// ctx.database is one (work.RequestDatabase).
type callableHandle struct{ calls []Value }

func (h *callableHandle) OnGet(key string) Value { return New("got " + key) }
func (h *callableHandle) OnCompare(string, Value) Value { return Value{K: Nil} }
func (h *callableHandle) OnInvoke(method string, args ...Value) Value {
	if method == "" {
		h.calls = append(h.calls, args...)
		return New("called")
	}
	return New("method " + method)
}

type hostWithHandle struct{ handle *callableHandle }

func (o *hostWithHandle) Handle() Value { return Value{K: Proxy, V: o.handle} }

// obj.handle(arg) reads the getter's proxy and calls it with the argument; obj.handle() and
// obj.handle are the proxy itself, as any getter's result.
func TestInvokeCallsAGetterProxyWithArguments(t *testing.T) {
	handle := &callableHandle{}
	host := New(&hostWithHandle{handle: handle})

	if got := host.Invoke("handle", New("ledger")); got.Text() != "called" || len(handle.calls) != 1 || handle.calls[0].Text() != "ledger" {
		t.Fatalf("obj.handle(\"ledger\") must call the proxy with the argument: got %v, calls %v", got, handle.calls)
	}
	if got := host.Invoke("handle"); got.K != Proxy || len(handle.calls) != 1 {
		t.Fatalf("obj.handle() is the proxy itself: got kind %v, calls %v", got.K, handle.calls)
	}
	if got := host.Get("handle"); got.K != Proxy {
		t.Fatalf("obj.handle is the proxy: got kind %v", got.K)
	}
	if got := host.Get("handle").Invoke("table", New("x")); got.Text() != "method table" {
		t.Fatalf("a method on the proxy still reaches OnInvoke(name): %v", got)
	}
}
