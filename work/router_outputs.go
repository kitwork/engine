package work

import (
	"fmt"
	"net/http"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/utilities/publishing"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func (t *Tenant) outputData(
	vm *runtime.VM,
	bytecode *compiler.Bytecode,
	method *FolderMethod,
	ctx *Context,
) (value.Value, error) {
	data := method.outputData
	if method.handle != nil {
		data = t.execTree(vm, bytecode, method.handle, ctx)
		if data.K == value.Invalid {
			return data, fmt.Errorf("generated %s provider failed: %v", method.outputKind, data.V)
		}
	}
	if !data.IsMap() {
		return data, nil
	}

	// Never mutate the declaration captured at folder compile time. Requests may run concurrently.
	resolved := make(map[string]value.Value, len(data.Map()))
	for key, item := range data.Map() {
		resolved[key] = item
	}
	for _, key := range []string{"items", "entries", "pages"} {
		item, ok := resolved[key]
		if !ok || item.K != value.Func {
			continue
		}
		result := t.execTree(vm, bytecode, lambdaOf(item), ctx)
		if result.K == value.Invalid {
			return result, fmt.Errorf("generated %s %s provider failed: %v", method.outputKind, key, result.V)
		}
		resolved[key] = result
	}
	return value.New(resolved), nil
}

func (t *Tenant) executeGeneratedOutput(
	vm *runtime.VM,
	bytecode *compiler.Bytecode,
	method *FolderMethod,
	ctx *Context,
) error {
	data, err := t.outputData(vm, bytecode, method, ctx)
	if err != nil {
		return err
	}
	request := ctx.Request()
	base := request.Scheme().String() + "://" + request.Host().String()

	switch method.outputKind {
	case "rss":
		document, err := publishing.RSS(data, base, ctx.Path().String())
		if err != nil {
			return err
		}
		response := ctx.Type(publishing.RSSMediaType)
		response.Header("ETag", publishing.ETag(document))
		if modified := publishing.LastModified(data); !modified.IsZero() {
			response.Header("Last-Modified", modified.Format(http.TimeFormat))
		}
		response.Send(value.New(document))
	case "sitemap":
		document, err := publishing.Sitemap(data, base, method.outputPath, ctx.Path().String())
		if err != nil {
			return err
		}
		response := ctx.Type(publishing.SitemapMediaType)
		response.Header("ETag", publishing.ETag(document))
		if modified := publishing.LastModified(data); !modified.IsZero() {
			response.Header("Last-Modified", modified.Format(http.TimeFormat))
		}
		response.Send(value.New(document))
	default:
		return fmt.Errorf("unknown generated output %q", method.outputKind)
	}
	return nil
}
