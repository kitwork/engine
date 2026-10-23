package compiler

import "fmt"

// kitwork/storage (KITWORK_STORAGE.md): memory(), disk() and datastore() make a handle; its value is read and
// written on the handle, memory("rates").expire("5m").get(() => …). The module has those three words and no
// other. It replaced cache of "kitwork" (cache.get, cache.set, cache.delete), and its send replaced sse of
// "kitwork" with context.sse (KITWORK_SSE.md): both are refused here, where they are written, in the words
// the runtime uses for them too.

// storageWords are the words of kitwork/storage.
var storageWords = map[string]bool{"memory": true, "disk": true, "datastore": true}

// CacheRemoved is the error of cache, read off "kitwork" until 22/10/2026 (import { cache } from "kitwork",
// kitwork().cache, kitwork/cache).
const CacheRemoved = `cache đã bỏ: import { memory } from "kitwork/storage"; cache.get("rates") là memory("rates").get(), cache.get(key, () => …) là memory("rates").get(() => …), cache.set(key, value, "5m") là memory("rates").expire("5m").set(value), cache.delete(key) là memory("rates").delete() (KITWORK_STORAGE.md mục 12)`

// SSERemoved is the error of sse, read off "kitwork" until 22/10/2026 (import { sse } from "kitwork",
// kitwork().sse, kitwork/sse).
const SSERemoved = `sse đã bỏ: luồng mở bằng context.sse trong router.get, tin đi vào bằng send của kitwork/storage: sse.connect({ channel: "room:1" }) là router.get((context) => context.sse.subscribe(memory("rooms").segment(1))), sse.publish("room:1", { event, id, data }) là memory("rooms").segment(1).send({ event, id, data }) (KITWORK_SSE.md mục 11)`

// SSEParameterRemoved is the error of a handler that takes sse after its context, (context, sse) => …, the
// helper a handler was handed until 22/10/2026.
const SSEParameterRemoved = `tham số sse đã bỏ: luồng là context.sse trong router.get, router.get((context) => { const stream = context.sse; …; return stream; }); tin đi vào bằng memory("rooms").segment(1).send({ event, id, data }) (KITWORK_SSE.md mục 11)`

// StorageUnknownWord is the error of a word kitwork/storage has not (storage.get, import { cache } from
// "kitwork/storage").
func StorageUnknownWord(word string) string {
	return fmt.Sprintf(`storage.%s không có: kitwork/storage có memory, disk, datastore; giá trị đọc ghi trên handle của chúng, memory("tên").get(), memory("tên").set(giá_trị) (KITWORK_STORAGE.md mục 2)`, word)
}

// removedKitworkWord is the error of a word "kitwork" had for storage and streams, "" for any other word.
func removedKitworkWord(word string) string {
	switch word {
	case "cache":
		return CacheRemoved
	case "sse":
		return SSERemoved
	}
	return ""
}

// markStorage marks a declared variable as the module kitwork/storage itself (import * as storage from
// "kitwork/storage", import { storage } from "kitwork").
func (r *resolver) markStorage(node *VarStatement, name *Identifier, declared *binding) {
	if declared == nil || node.Pattern != nil {
		return
	}
	if module, ok := kitworkModulePath(node.Value); ok {
		declared.storageModule = (node.DestructMode == DestructNone && module == "storage") ||
			(node.DestructMode == DestructObject && module == "" && name.Value == "storage")
	}
}

// storageDeclaration refuses an import of cache or sse (import { cache } from "kitwork", anything of
// kitwork/cache or kitwork/sse), and of a word kitwork/storage has not (import { get } from
// "kitwork/storage", const { get } = storage).
func (r *resolver) storageDeclaration(node *VarStatement) {
	var words []*Identifier
	module, isModule := kitworkModulePath(node.Value)
	switch {
	case node.Pattern != nil:
		namespace, isName := node.Value.(*Identifier)
		if !isName || node.Pattern.Array {
			return
		}
		if found := r.lookup(namespace.Value); found == nil || !found.storageModule {
			return
		}
		for _, element := range node.Pattern.Elements {
			words = append(words, &Identifier{Token: element.KeyToken, Value: element.Key})
		}
		module = "storage"
	case isModule && (module == "cache" || module == "sse"):
		if len(node.Names) > 0 {
			r.fail(fmt.Errorf("%s: %s", r.where(node.Names[0].Token), removedKitworkWord(module)))
		}
		return
	case isModule && node.DestructMode == DestructObject && (module == "" || module == "storage"):
		words = node.Names
	default:
		return
	}
	for _, word := range words {
		message := ""
		if module == "" {
			message = removedKitworkWord(word.Value)
		} else if !storageWords[word.Value] {
			message = StorageUnknownWord(word.Value)
		}
		if message != "" {
			r.fail(fmt.Errorf("%s: %s", r.where(word.Token), message))
			return
		}
	}
}

// storageMember refuses, where it is written, kitwork().cache and kitwork().sse (import { cache as c } from
// "kitwork" lowers to the first), and a word read off kitwork/storage that it has not (storage.get).
func (r *resolver) storageMember(object Expression, property *Identifier) bool {
	if property == nil {
		return false
	}
	if isKitworkCall(object) {
		if message := removedKitworkWord(property.Value); message != "" {
			r.fail(fmt.Errorf("%s: %s", r.where(property.Token), message))
			return true
		}
		return false
	}
	module, isModule := kitworkModulePath(object)
	if name, isName := object.(*Identifier); isName {
		found := r.lookup(name.Value)
		isModule, module = found != nil && found.storageModule, "storage"
	}
	if isModule && module == "storage" && !storageWords[property.Value] {
		r.fail(fmt.Errorf("%s: %s", r.where(property.Token), StorageUnknownWord(property.Value)))
		return true
	}
	return false
}
