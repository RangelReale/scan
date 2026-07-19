package scan

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// countingDest is a sql.Scanner destination that counts every scan, so the
// tests can assert exactly which columns were decoded.
type countingDest struct {
	V     any
	scans *int
}

func (d *countingDest) Scan(src any) error {
	*d.scans++

	switch v := d.V.(type) {
	case *string:
		switch s := src.(type) {
		case string:
			*v = s
		case []byte:
			*v = string(s)
		default:
			return fmt.Errorf("cannot scan %T into *string", src)
		}
	default:
		return fmt.Errorf("unsupported destination %T", d.V)
	}

	return nil
}

type countingConverter struct {
	scans *int
}

func (c countingConverter) TypeToDestination(typ reflect.Type) reflect.Value {
	return reflect.ValueOf(&countingDest{V: reflect.New(typ).Interface(), scans: c.scans})
}

func (c countingConverter) ValueFromDestination(val reflect.Value) reflect.Value {
	return val.Elem().FieldByName("V").Elem().Elem()
}

// plainConverter produces destinations that do NOT implement sql.Scanner, to
// exercise the graceful degradation path.
type plainConverter struct{}

func (plainConverter) TypeToDestination(typ reflect.Type) reflect.Value {
	return reflect.New(typ)
}

func (plainConverter) ValueFromDestination(val reflect.Value) reflect.Value {
	return val.Elem()
}

type skipRow struct {
	Key string `db:"key"`
	A   string `db:"a"`
	B   string `db:"b"`
}

func skipTestKey(vals []reflect.Value) string {
	return *vals[0].Interface().(*countingDest).V.(*string)
}

// firstSeen returns a seen callback that reports a key as known from its
// second occurrence onward — the same timing as an external cache that is
// filled when a fresh row is consumed.
func firstSeen() func([]reflect.Value) bool {
	known := make(map[string]struct{})
	return func(vals []reflect.Value) bool {
		k := skipTestKey(vals)
		if _, ok := known[k]; ok {
			return true
		}
		known[k] = struct{}{}
		return false
	}
}

var (
	skipTestCols = [][2]string{{"key", "string"}, {"a", "string"}, {"b", "string"}}
	skipTestRows = [][]any{
		{"k1", "a1", "b1"},
		{"k1", "a2", "b2"},
		{"k2", "a3", "b3"},
		{"k1", "a4", "b4"},
		{"k2", "a5", "b5"},
	}
	skipTestFull = []skipRow{
		{Key: "k1", A: "a1", B: "b1"},
		{Key: "k1", A: "a2", B: "b2"},
		{Key: "k2", A: "a3", B: "b3"},
		{Key: "k1", A: "a4", B: "b4"},
		{Key: "k2", A: "a5", B: "b5"},
	}
)

// runRowSkip queries the test table with a counting converter and the given
// extra options, returning the mapped rows and the number of column scans.
func runRowSkip(t *testing.T, queryCols []string, opts ...MappingOption) ([]skipRow, int) {
	t.Helper()

	db, cleanup := createDB(t, skipTestCols)
	defer cleanup()
	insert(t, db, []string{"key", "a", "b"}, skipTestRows...)

	scans := 0
	allOpts := append(
		[]MappingOption{WithTypeConverter(countingConverter{scans: &scans})},
		opts...,
	)

	rows, err := db.QueryContext(context.Background(), createQuery(t, queryCols))
	if err != nil {
		t.Fatal(err)
	}

	got, err := AllFromRows(context.Background(), StructMapper[skipRow](allOpts...), rows)
	if err != nil {
		t.Fatal(err)
	}

	return got, scans
}

func TestRowSkipSkipsKnownRows(t *testing.T) {
	got, scans := runRowSkip(t, []string{"key", "a", "b"},
		WithRowSkip([]string{"key"}, firstSeen()))

	want := []skipRow{
		{Key: "k1", A: "a1", B: "b1"},
		{Key: "k1"}, // known: only the key is decoded
		{Key: "k2", A: "a3", B: "b3"},
		{Key: "k1"},
		{Key: "k2"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error(diff)
	}

	// the key always decodes (5 rows) and a/b only decode on the two
	// first-seen rows (2 rows x 2 columns)
	if scans != 9 {
		t.Errorf("expected 9 column scans, got %d", scans)
	}
}

func TestRowSkipKeyAfterOtherColumns(t *testing.T) {
	// the key is the last column of the result set, so no column can react
	// to the decision: everything is decoded, results are unchanged
	got, scans := runRowSkip(t, []string{"a", "b", "key"},
		WithRowSkip([]string{"key"}, firstSeen()))

	if diff := cmp.Diff(skipTestFull, got); diff != "" {
		t.Error(diff)
	}
	if scans != 15 {
		t.Errorf("expected 15 column scans, got %d", scans)
	}
}

func TestRowSkipUnknownKeyColumn(t *testing.T) {
	got, scans := runRowSkip(t, []string{"key", "a", "b"},
		WithRowSkip([]string{"nope"}, firstSeen()))

	if diff := cmp.Diff(skipTestFull, got); diff != "" {
		t.Error(diff)
	}
	if scans != 15 {
		t.Errorf("expected 15 column scans, got %d", scans)
	}
}

func TestRowSkipNeverSeen(t *testing.T) {
	got, scans := runRowSkip(t, []string{"key", "a", "b"},
		WithRowSkip([]string{"key"}, func([]reflect.Value) bool { return false }))

	if diff := cmp.Diff(skipTestFull, got); diff != "" {
		t.Error(diff)
	}
	if scans != 15 {
		t.Errorf("expected 15 column scans, got %d", scans)
	}
}

func TestRowSkipNonScannerDestinations(t *testing.T) {
	// plainConverter destinations don't implement sql.Scanner, so skipping
	// cannot delegate and every column is decoded normally — even though
	// seen always reports the row as known
	db, cleanup := createDB(t, skipTestCols)
	defer cleanup()
	insert(t, db, []string{"key", "a", "b"}, skipTestRows...)

	rows, err := db.QueryContext(context.Background(), createQuery(t, []string{"key", "a", "b"}))
	if err != nil {
		t.Fatal(err)
	}

	got, err := AllFromRows(context.Background(), StructMapper[skipRow](
		WithTypeConverter(plainConverter{}),
		WithRowSkip([]string{"key"}, func([]reflect.Value) bool { return true }),
	), rows)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(skipTestFull, got); diff != "" {
		t.Error(diff)
	}
}
