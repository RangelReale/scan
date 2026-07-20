package scan

import (
	"context"
	"fmt"
	"reflect"
)

// Uses reflection to create a mapping function for a struct type
// using the default options
func StructConfigMapper[T any](opts ...MappingConfigOption) Mapper[T] {
	return CustomStructConfigMapper[T](defaultStructMapper, opts...)
}

func StructConfigMapperColumns[T any](opts ...MappingConfigOption) ([]string, error) {
	return CustomStructMapperConfigColumns[T](defaultStructMapper, opts...)
}

// Uses reflection to create a mapping function for a struct type
// using with custom options
func CustomStructConfigMapper[T any](src StructMapperSource, optMod ...MappingConfigOption) Mapper[T] {
	opts := mappingConfigOptions{}
	for _, o := range optMod {
		o(&opts)
	}

	mod := func(ctx context.Context, c cols) (func(*Row) (any, error), func(any) (T, error)) {
		return structMapperConfigFrom[T](ctx, c, src, opts)
	}

	if len(opts.mapperMods) > 0 {
		mod = Mod(mod, opts.mapperMods...)
	}

	return mod
}

func CustomStructMapperConfigColumns[T any](src StructMapperSource, optMod ...MappingConfigOption) ([]string, error) {
	opts := mappingConfigOptions{}
	for _, o := range optMod {
		o(&opts)
	}

	if len(opts.mapperMods) > 0 {
		return nil, fmt.Errorf("Mapper mods are not supported in CustomStructMapperColumns")
	}

	typ := typeOf[T]()

	_, err := checks(typ)
	if err != nil {
		return nil, err
	}

	mapping, err := src.getMapping(typ)
	if err != nil {
		return nil, err
	}

	return mapping.cols(), nil
}

func structMapperConfigFrom[T any](ctx context.Context, c cols, s StructMapperSource, opts mappingConfigOptions) (func(*Row) (any, error), func(any) (T, error)) {
	typ := typeOf[T]()

	isPointer, err := checks(typ)
	if err != nil {
		return ErrorMapper[T](err)
	}

	mapping, err := s.getMapping(typ)
	if err != nil {
		return ErrorMapper[T](err)
	}

	return mapperConfigFromMapping[T](mapping, typ, isPointer, opts)(ctx, c)
}

type mappingConfigOptions struct {
	typeConverter   TypeConverter
	rowValidator    RowValidator
	mapperMods      []MapperMod
	structTagPrefix string
	// rowSkipKeys     []string
	// rowSkipSeen     func([]reflect.Value) bool
}

// MappingConfigeOption is a function type that changes how the mapper is generated
type MappingConfigOption func(*mappingConfigOptions)

// WithRowValidator sets the [RowValidator] for the struct mapper
// after scanning all values in a row, they are passed to the RowValidator
// if it returns false, the zero value for that row is returned
func WithMappingConfigRowValidator(rv RowValidator) MappingConfigOption {
	return func(opt *mappingConfigOptions) {
		opt.rowValidator = rv
	}
}

// TypeConverter sets the [TypeConverter] for the struct mapper
// it is called to modify the type of a column and get the original value back
func WithTMappingConfigypeConverter(tc TypeConverter) MappingConfigOption {
	return func(opt *mappingConfigOptions) {
		opt.typeConverter = tc
	}
}

// WithStructTagPrefix should be used when every column from the database has a prefix.
func WithMappingConfigStructTagPrefix(prefix string) MappingConfigOption {
	return func(opt *mappingConfigOptions) {
		opt.structTagPrefix = prefix
	}
}

// WithMapperMods accepts mods used to modify the mapper
func WithMappingConfigMapperMods(mods ...MapperMod) MappingConfigOption {
	return func(opt *mappingConfigOptions) {
		opt.mapperMods = append(opt.mapperMods, mods...)
	}
}

func mapperConfigFromMapping[T any](m mapping, typ reflect.Type, isPointer bool, opts mappingConfigOptions) func(context.Context, cols) (func(*Row) (any, error), func(any) (T, error)) {
	return func(ctx context.Context, c cols) (func(*Row) (any, error), func(any) (T, error)) {
		// Filter the mapping so we only ask for the available columns
		filtered, err := filterColumns(c, m, opts.structTagPrefix)
		if err != nil {
			return ErrorMapper[T](err)
		}

		mapper := regularConfig[T]{
			typ:       typ,
			isPointer: isPointer,
			filtered:  filtered,
			converter: opts.typeConverter,
			validator: opts.rowValidator,
			// rowSkipKeys: opts.rowSkipKeys,
			// rowSkipSeen: opts.rowSkipSeen,
		}
		switch {
		case opts.typeConverter == nil && opts.rowValidator == nil:
			return mapper.regular()

		default:
			return mapper.allOptions()
		}
	}
}

type regularConfig[T any] struct {
	isPointer bool
	typ       reflect.Type
	filtered  mapping
	converter TypeConverter
	validator RowValidator
	// rowSkipKeys []string
	// rowSkipSeen func([]reflect.Value) bool
}

func (s regularConfig[T]) regular() (func(*Row) (any, error), func(any) (T, error)) {
	// The mappingConfig is fixed for the duration of the query, so everything that
	// only depends on it is resolved here, once, instead of once per row.
	styp := s.typ
	if s.isPointer {
		styp = s.typ.Elem()
	}
	inits := uniqueInits(s.filtered)

	// scanOneRow calls before/scan/after strictly in sequence for each row
	// and the mapper is built once per query, so the link holder can be
	// reused across rows, avoiding a per-row boxing allocation.
	link := new(rowLink)

	return func(v *Row) (any, error) {
			row := reflect.New(styp).Elem()

			// row is freshly zero, so each unique nested pointer is
			// initialized exactly once, ancestors before descendants,
			// before any field address is scheduled
			for _, path := range inits {
				pv := row.FieldByIndex(path)
				pv.Set(reflect.New(pv.Type().Elem()))
			}

			for _, info := range s.filtered {
				fv := row.FieldByIndex(info.position)
				v.ScheduleScanByIndexX(info.colIndex, fv.Addr())
			}

			link.v = row

			return link, nil
		}, func(v any) (T, error) {
			row := v.(*rowLink).v

			if s.isPointer {
				row = row.Addr()
			}

			return row.Interface().(T), nil
		}
}

func (s regularConfig[T]) allOptions() (func(*Row) (any, error), func(any) (T, error)) {
	// The mapping is fixed for the duration of the query: the field types,
	// the validator's column names and the nested-pointer init paths are all
	// resolved here, once, instead of once per row (per column).
	styp := s.typ
	if s.isPointer {
		styp = s.typ.Elem()
	}

	ftypes := make([]reflect.Type, len(s.filtered))
	for i, info := range s.filtered {
		ftypes[i] = styp.FieldByIndex(info.position).Type
	}

	colNames := s.filtered.cols()
	inits := uniqueInits(s.filtered)

	// scanOneRow calls before/scan/after strictly in sequence for each row
	// and the mapper is built once per query, so the destinations slice can
	// be reused across rows — like the links slice in [Mod] and the scan
	// buffers in [Row]. Boxing it once also avoids a per-row allocation.
	scratch := make([]reflect.Value, len(s.filtered))
	var link any = scratch

	makeDest := func(i int) reflect.Value {
		if s.converter != nil {
			return s.converter.TypeToDestination(ftypes[i])
		}
		return reflect.New(ftypes[i])
	}

	// skip := buildRowSkipPlan(s.filtered, s.rowSkipKeys, s.rowSkipSeen)
	// if skip != nil {
	// 	skip.makeDest = makeDest
	// 	skip.scratch = scratch
	// 	// delegating a non-skipped column's scan needs the destination to
	// 	// be a sql.Scanner; probe one to decide for the query
	// 	if _, ok := makeDest(skip.conds[0].idx).Interface().(sql.Scanner); !ok {
	// 		skip = nil
	// 	}
	// }

	return func(v *Row) (any, error) {
			// if skip != nil {
			// 	skip.state.decided = false
			// }

			for i, info := range s.filtered {
				// if skip != nil {
				// 	if slot := skip.condSlot[i]; slot >= 0 {
				// 		// the destination is created lazily by the
				// 		// condDest, and only when the row is not skipped
				// 		v.ScheduleScanByIndex(info.colIndex, &skip.conds[slot])
				// 		continue
				// 	}
				// }

				scratch[i] = makeDest(i)

				// if skip != nil {
				// 	if slot := skip.keySlot[i]; slot >= 0 {
				// 		skip.keyVals[slot] = scratch[i]
				// 	}
				// }

				v.ScheduleScanByIndexX(info.colIndex, scratch[i])
			}

			return link, nil
		}, func(v any) (T, error) {
			vals := v.([]reflect.Value)

			// if skip != nil && skip.skipped() {
			// 	// the row is already known: only the key columns were
			// 	// decoded, so build a value carrying just those — the
			// 	// caller promised to substitute its own copy of the row
			// 	row := reflect.New(styp).Elem()
			// 	for _, path := range inits {
			// 		pv := row.FieldByIndex(path)
			// 		pv.Set(reflect.New(pv.Type().Elem()))
			// 	}
			//
			// 	for i, info := range s.filtered {
			// 		if skip.keySlot[i] < 0 {
			// 			continue
			// 		}
			//
			// 		var val reflect.Value
			// 		if s.converter != nil {
			// 			val = s.converter.ValueFromDestination(vals[i])
			// 		} else {
			// 			val = vals[i].Elem()
			// 		}
			//
			// 		fv := row.FieldByIndex(info.position)
			// 		if info.isPointer {
			// 			fv.Elem().Set(val)
			// 		} else {
			// 			fv.Set(val)
			// 		}
			// 	}
			//
			// 	if s.isPointer {
			// 		row = row.Addr()
			// 	}
			//
			// 	return row.Interface().(T), nil
			// }

			if s.validator != nil && !s.validator(colNames, vals) {
				var t T
				return t, nil
			}

			row := reflect.New(styp).Elem()

			// row is freshly zero, so each unique nested pointer is
			// initialized exactly once, ancestors before descendants
			for _, path := range inits {
				pv := row.FieldByIndex(path)
				pv.Set(reflect.New(pv.Type().Elem()))
			}

			for i, info := range s.filtered {
				var val reflect.Value
				if s.converter != nil {
					val = s.converter.ValueFromDestination(vals[i])
				} else {
					val = vals[i].Elem()
				}

				fv := row.FieldByIndex(info.position)
				if info.isPointer {
					fv.Elem().Set(val)
				} else {
					fv.Set(val)
				}
			}

			if s.isPointer {
				row = row.Addr()
			}

			return row.Interface().(T), nil
		}
}
