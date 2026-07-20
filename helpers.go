package scan

import (
	"reflect"
	"strings"
)

func typeOf[T any]() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

type tagOptions struct {
	Name string            // the first positional segment
	Attr map[string]string // the rest, as name=value
}

// parseTag parses "name,name=val,name2=val2" into Name + Attr.
func parseTag(tag string) tagOptions {
	opts := tagOptions{Attr: make(map[string]string)}

	// Cut off the first segment as Name; parse only the remainder.
	first, rest, _ := strings.Cut(tag, ",")
	opts.Name = strings.TrimSpace(first)

	if rest == "" {
		return opts
	}
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if k, v, found := strings.Cut(part, "="); found {
			opts.Attr[strings.TrimSpace(k)] = strings.TrimSpace(v)
		} else {
			opts.Attr[part] = "" // bare flag in the tail, e.g. "required"
		}
	}
	return opts
}
