package agent

import (
	"encoding/csv"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	starjson "go.starlark.net/lib/json"
	starmath "go.starlark.net/lib/math"
	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// scriptLib is what every calc script has besides the document tools:
// json, math, time and the helpers below. The time module has no now(): a
// script sees only the dates it is given, and the task says what day it is.
func scriptLib() starlark.StringDict {
	members := starlark.StringDict{}
	for name, v := range startime.Module.Members {
		if name != "now" {
			members[name] = v
		}
	}
	return starlark.StringDict{
		"json":           starjson.Module,
		"math":           starmath.Module,
		"time":           &starlarkstruct.Module{Name: "time", Members: members},
		"normalize_name": starlark.NewBuiltin("normalize_name", normalizeNameFn),
		"count_by":       starlark.NewBuiltin("count_by", countByFn),
		"table":          starlark.NewBuiltin("table", tableFn),
	}
}

var listNumber = regexp.MustCompile(`^\d+[.)]\s+`)

// normalizeName cleans one line of an extracted list: bullets and list
// numbers go, white space collapses, trailing separators go.
func normalizeName(s string) string {
	s = strings.TrimLeft(strings.TrimSpace(s), "-*+•–— \t")
	s = listNumber.ReplaceAllString(s, "")
	return strings.TrimRight(strings.Join(strings.Fields(s), " "), ",;:")
}

func normalizeNameFn(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &s); err != nil {
		return nil, err
	}
	return starlark.String(normalizeName(s)), nil
}

// countByFn counts the items of an iterable, by key(item) if a key function
// is given. The result is a dict ordered by count, highest first, then by
// key.
func countByFn(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var items starlark.Iterable
	var key starlark.Callable
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "items", &items, "key?", &key); err != nil {
		return nil, err
	}
	counts := starlark.NewDict(16)
	it := items.Iterate()
	defer it.Done()
	var x starlark.Value
	for it.Next(&x) {
		k := x
		if key != nil {
			var err error
			if k, err = starlark.Call(thread, key, starlark.Tuple{x}, nil); err != nil {
				return nil, err
			}
		}
		n := 0
		if v, ok, _ := counts.Get(k); ok {
			n, _ = starlark.AsInt32(v)
		}
		if err := counts.SetKey(k, starlark.MakeInt(n+1)); err != nil {
			return nil, fmt.Errorf("%s: %w", b.Name(), err)
		}
	}
	entries := counts.Items()
	sort.SliceStable(entries, func(i, j int) bool {
		ci, _ := starlark.AsInt32(entries[i][1])
		cj, _ := starlark.AsInt32(entries[j][1])
		if ci != cj {
			return ci > cj
		}
		return entries[i][0].String() < entries[j][0].String()
	})
	sorted := starlark.NewDict(len(entries))
	for _, e := range entries {
		_ = sorted.SetKey(e[0], e[1])
	}
	return sorted, nil
}

// tableFn renders rows (dicts, or lists with columns given) as a Markdown
// table or as CSV text.
func tableFn(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var rows starlark.Iterable
	var columns *starlark.List
	format := "markdown"
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "rows", &rows, "columns?", &columns, "format?", &format); err != nil {
		return nil, err
	}
	if format != "markdown" && format != "csv" {
		return nil, fmt.Errorf("%s: format must be \"markdown\" or \"csv\", not %q", b.Name(), format)
	}
	var cols []string
	if columns != nil {
		for i := 0; i < columns.Len(); i++ {
			cols = append(cols, cellText(columns.Index(i)))
		}
	}
	var body [][]string
	it := rows.Iterate()
	defer it.Done()
	var row starlark.Value
	for it.Next(&row) {
		switch r := row.(type) {
		case *starlark.Dict:
			if columns == nil { // columns as first seen, in order
				for _, k := range r.Keys() {
					if name := cellText(k); !slices.Contains(cols, name) {
						cols = append(cols, name)
					}
				}
			}
			cells := make([]string, len(cols))
			for i, c := range cols {
				if v, ok, _ := r.Get(starlark.String(c)); ok {
					cells[i] = cellText(v)
				}
			}
			body = append(body, cells)
		case starlark.Indexable:
			if columns == nil {
				return nil, fmt.Errorf("%s: rows that are lists need columns=[...]", b.Name())
			}
			cells := make([]string, len(cols))
			for i := 0; i < len(cols) && i < r.Len(); i++ {
				cells[i] = cellText(r.Index(i))
			}
			body = append(body, cells)
		default:
			return nil, fmt.Errorf("%s: a row is a dict or a list, not %s", b.Name(), row.Type())
		}
	}
	// Dict rows seen before a later row added a column are padded.
	for i, cells := range body {
		for len(cells) < len(cols) {
			cells = append(cells, "")
		}
		body[i] = cells
	}
	var sb strings.Builder
	if format == "csv" {
		w := csv.NewWriter(&sb)
		_ = w.Write(cols)
		_ = w.WriteAll(body)
		w.Flush()
		return starlark.String(sb.String()), nil
	}
	line := func(cells []string) {
		sb.WriteString("|")
		for _, c := range cells {
			sb.WriteString(" " + strings.NewReplacer("|", `\|`, "\n", " ").Replace(c) + " |")
		}
		sb.WriteString("\n")
	}
	line(cols)
	sb.WriteString("|" + strings.Repeat(" --- |", len(cols)) + "\n")
	for _, cells := range body {
		line(cells)
	}
	return starlark.String(sb.String()), nil
}

// cellText shows a value in a table: strings as they are, None as empty.
func cellText(v starlark.Value) string {
	switch v := v.(type) {
	case starlark.String:
		return string(v)
	case starlark.NoneType:
		return ""
	}
	return v.String()
}
