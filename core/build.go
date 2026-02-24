package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

func (e *Engine) Build(source string, tenantID string, domain string, sourcePath string) (*work.Work, error) {
	l := compiler.NewLexer(source)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, errors.New(p.Errors()[0])
	}

	w := work.New("temp")
	w.Entity = tenantID
	w.Domain = domain
	w.SourcePath = sourcePath

	env := compiler.NewEnclosedEnvironment(e.stdlib)
	env.Set("work", value.NewFunc(func(args ...value.Value) value.Value {
		name := "unnamed"
		if len(args) > 0 {
			name = args[0].Text()
		}
		if existing, ok := e.Registry[name]; ok {
			w = existing
			return value.New(existing)
		}
		w.Name = name
		w.Entity = tenantID
		w.Domain = domain
		w.SourcePath = sourcePath
		e.Registry[name] = w
		return value.New(w)
	}))

	env.Set("router", value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) < 2 {
			return value.NewNull()
		}
		method := args[0].Text()
		path := args[1].Text()

		name := fmt.Sprintf("rt_%s_%s_%s_%s", tenantID, domain, method, path)
		wTmp := work.New(name)
		wTmp.Entity = tenantID
		wTmp.Domain = domain
		wTmp.SourcePath = w.SourcePath

		r := &work.Router{
			Work:   *wTmp,
			Method: method,
			Path:   path,
		}

		e.RegistryMu.Lock()
		e.Routers = append(e.Routers, r)
		e.RegistryMu.Unlock()
		return value.New(r)
	}))

	// --- Fluent API Implementation ---

	// 1. Router Object
	routerObj := make(map[string]value.Value)
	routerObj["asset"] = value.NewFunc(func(args ...value.Value) value.Value {
		prefix := ""
		if len(args) > 0 {
			prefix = args[0].Text()
		}
		assetObj := make(map[string]value.Value)
		assetObj["static"] = value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) < 2 {
				return value.NewNull()
			}
			routePath := args[0].Text()
			dir := args[1].Text()
			e.RegistryMu.Lock()
			e.Config.Assets = append(e.Config.Assets, Asset{Path: prefix + routePath, Dir: dir})
			e.RegistryMu.Unlock()
			return value.NewNull()
		})
		return value.New(assetObj)
	})

	routerObj["group"] = value.NewFunc(func(args ...value.Value) value.Value {
		groupPrefix := ""
		if len(args) > 0 {
			groupPrefix = args[0].Text()
		}
		groupObj := make(map[string]value.Value)

		createGroupMethod := func(meth string) value.Value {
			return value.NewFunc(func(args ...value.Value) value.Value {
				if len(args) < 1 {
					return value.NewNull()
				}
				subPath := args[0].Text()
				fullPath := groupPrefix + subPath
				name := fmt.Sprintf("rt_%s_%s", meth, fullPath)
				wTmp := work.New(name)
				wTmp.Entity = tenantID
				wTmp.Domain = domain
				wTmp.SourcePath = w.SourcePath
				r := &work.Router{Work: *wTmp, Method: meth, Path: fullPath}

				routeObj := make(map[string]value.Value)
				routeObj["handle"] = value.NewFunc(func(args ...value.Value) value.Value {
					if len(args) > 0 {
						r.Done(args[0])
					}
					return value.New(r)
				})
				e.RegistryMu.Lock()
				e.Routers = append(e.Routers, r)
				e.RegistryMu.Unlock()
				return value.New(routeObj)
			})
		}
		groupObj["get"] = createGroupMethod("GET")
		groupObj["post"] = createGroupMethod("POST")
		return value.New(groupObj)
	})
	env.Set("router", value.New(routerObj))

	// 2. Render Object
	renderObj := make(map[string]value.Value)
	renderObj["template"] = value.NewFunc(func(args ...value.Value) value.Value {
		shellPath := ""
		if len(args) > 0 {
			shellPath = args[0].Text()
		}
		tmplObj := make(map[string]value.Value)
		tmplObj["page"] = value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) < 1 {
				return value.NewNull()
			}
			routePath := args[0].Text()

			if e.Config.Debug {
				fmt.Printf("[DEBUG] page() called: path=%s, argsCount=%d\n", routePath, len(args))
				for i, a := range args {
					fmt.Printf("  arg[%d]: %s\n", i, a.Text())
				}
			}

			// Auto-infer or use provided page file
			pageFile := ""
			if len(args) > 1 {
				pageFile = args[1].Text()
			} else {
				pageFile = strings.TrimPrefix(routePath, "/")
				if pageFile == "" {
					pageFile = "page.html"
				} else {
					if !strings.HasSuffix(pageFile, ".html") {
						pageFile = filepath.Join(pageFile, "page.html")
					}
				}
			}

			wTmp := work.New("page_" + routePath)
			wTmp.Entity = tenantID
			wTmp.Domain = domain
			wTmp.ShellPath = shellPath
			wTmp.TemplatePath = pageFile
			wTmp.SourcePath = w.SourcePath
			r := &work.Router{Work: *wTmp, Method: "GET", Path: routePath}

			if e.Config.Debug {
				fmt.Printf("[Fluent] Registered Page: %s -> Fragment: %s, Shell: %s\n", routePath, pageFile, shellPath)
			}

			pageObj := make(map[string]value.Value)
			pageObj["scope"] = value.NewFunc(func(args ...value.Value) value.Value {
				if len(args) > 0 {
					data := args[0]
					r.Done(value.NewFunc(func(innerArgs ...value.Value) value.Value { return data }))
				}
				return value.New(r)
			})
			pageObj["handle"] = value.NewFunc(func(args ...value.Value) value.Value {
				if len(args) > 0 {
					r.Done(args[0])
				}
				return value.New(r)
			})
			e.RegistryMu.Lock()
			e.Routers = append(e.Routers, r)
			e.RegistryMu.Unlock()
			return value.New(pageObj)
		})
		return value.New(tmplObj)
	})
	env.Set("render", value.New(renderObj))

	env.Set("cron", value.NewFunc(func(args ...value.Value) value.Value {
		name := "cron_job"
		if len(args) > 0 {
			name = args[0].Text()
		}

		wTmp := work.New(name)
		wTmp.Entity = tenantID
		wTmp.Domain = domain

		c := &work.Cron{
			Work: *wTmp,
		}

		e.RegistryMu.Lock()
		e.Crons = append(e.Crons, c)
		e.RegistryMu.Unlock()
		return value.New(c)
	}))

	c := e.compilerPool.Get().(*compiler.Compiler)
	c.Reset()
	if err := c.Compile(prog); err == nil {
		w.SetBytecode(c.ByteCodeResult())
		if e.Config.Debug {
			fmt.Printf("[Build] Assigned bytecode to Work: %s (bytecode length: %d)\n", w.Name, len(w.GetBytecode().Instructions))
		}
	} else {
		fmt.Printf("[Build] Compile error for Work: %s - %v\n", w.Name, err)
	}

	compiler.Evaluator(prog, env) // Now can read addresses from AST

	e.compilerPool.Put(c)
	e.cache.Add(source, w)
	return w, nil
}
