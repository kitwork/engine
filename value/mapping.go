package value

import (
	"fmt"
	"reflect"
	"strings"
)

// To maps the Value into a Go target (must be a pointer)
func (v Value) To(target any) error {
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer")
	}

	return v.mapTo(rv.Elem())
}

func (v Value) mapTo(target reflect.Value) error {
	// 1. Handle Nil Value
	if v.K == Nil {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}

	t := target.Type()

	// 2. Tự động khởi tạo và đi sâu vào con trỏ (e.g. *struct, *string)
	if target.Kind() == reflect.Ptr {
		if target.IsNil() {
			target.Set(reflect.New(t.Elem()))
		}
		return v.mapTo(target.Elem())
	}

	// 3. Mapping dựa trên Kind của Target
	switch target.Kind() {
	case reflect.Struct:
		if v.K != Map {
			return fmt.Errorf("cannot map %s to struct", v.K.String())
		}
		m := v.Map()
		if m == nil {
			return nil
		}

		for i := 0; i < target.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue // Bỏ qua unexported fields
			}

			// Tìm key: Ưu tiên yaml -> json -> Tên Field
			key := field.Name
			if tag := field.Tag.Get("yaml"); tag != "" {
				key = strings.Split(tag, ",")[0]
			} else if tag := field.Tag.Get("json"); tag != "" {
				key = strings.Split(tag, ",")[0]
			}

			if val, ok := m[key]; ok {
				if err := val.mapTo(target.Field(i)); err != nil {
					return err
				}
			} else if val, ok := m[strings.ToLower(key)]; ok {
				// Fallback cho viết thường
				if err := val.mapTo(target.Field(i)); err != nil {
					return err
				}
			}
		}

	case reflect.Slice:
		if v.K != Array {
			return fmt.Errorf("cannot map %s to slice", v.K.String())
		}
		arr := v.Array()
		slice := reflect.MakeSlice(t, len(arr), len(arr))
		for i, item := range arr {
			if err := item.mapTo(slice.Index(i)); err != nil {
				return err
			}
		}
		target.Set(slice)

	case reflect.String:
		target.SetString(v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		target.SetInt(int64(v.N))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		target.SetUint(uint64(v.N))
	case reflect.Float32, reflect.Float64:
		target.SetFloat(v.N)
	case reflect.Bool:
		target.SetBool(v.Truthy())
	case reflect.Interface:
		target.Set(reflect.ValueOf(v.Interface()))
	default:
		return fmt.Errorf("unsupported target kind: %s", target.Kind())
	}

	return nil
}
