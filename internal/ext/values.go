package ext

import (
	"fmt"
	"sort"

	rt "github.com/arnodel/golua/runtime"
)

// toLua converts Go data (strings, numbers, bools, slices, maps) into Lua
// values: a slice becomes a sequence, a map a table with sorted keys.
func toLua(v any) rt.Value {
	switch x := v.(type) {
	case nil:
		return rt.NilValue
	case rt.Value:
		return x
	case string:
		return rt.StringValue(x)
	case bool:
		return rt.BoolValue(x)
	case int:
		return rt.IntValue(int64(x))
	case int64:
		return rt.IntValue(x)
	case float64:
		return rt.FloatValue(x)
	case []string:
		t := rt.NewTable()
		for i, s := range x {
			t.Set(rt.IntValue(int64(i+1)), rt.StringValue(s))
		}
		return rt.TableValue(t)
	case []any:
		t := rt.NewTable()
		for i, e := range x {
			t.Set(rt.IntValue(int64(i+1)), toLua(e))
		}
		return rt.TableValue(t)
	case []map[string]any:
		t := rt.NewTable()
		for i, e := range x {
			t.Set(rt.IntValue(int64(i+1)), toLua(e))
		}
		return rt.TableValue(t)
	case map[string]any:
		t := rt.NewTable()
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.Set(rt.StringValue(k), toLua(x[k]))
		}
		return rt.TableValue(t)
	}
	return rt.StringValue(fmt.Sprint(v))
}
