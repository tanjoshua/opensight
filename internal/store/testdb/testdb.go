// Package testdb routes integration-test fixture and assertion operations
// through the test-only sqlc package. Query is an opaque catalog identifier;
// this API deliberately accepts no SQL text.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"opensight/internal/store/testsql"

	"github.com/jackc/pgx/v5/pgconn"
)

type Query int

const (
	Query001 Query = iota + 1
	Query002
	Query003
	Query004
	Query005
	Query006
	Query007
	Query008
	Query009
	Query010
	Query011
	Query012
	Query013
	Query014
	Query015
	Query016
	Query017
	Query018
	Query019
	Query020
	Query021
	Query022
	Query023
	Query024
	Query025
	Query026
	Query027
	Query028
	Query029
	Query030
	Query031
	Query032
	Query033
	Query034
	Query035
	Query036
	Query037
	Query038
	Query039
	Query040
	Query041
	Query042
	Query043
	Query044
	Query045
	Query046
	Query047
	Query048
	Query049
	Query050
	Query051
	Query052
	Query053
	Query054
	Query055
	Query056
	Query057
	Query058
	Query059
	Query060
	Query061
	Query062
	Query063
	Query064
	Query065
	Query066
	Query067
	Query068
	Query069
	Query070
	Query071
	Query072
	Query073
	Query074
	Query075
	Query076
	Query077
	Query078
	Query079
	Query080
	Query081
	Query082
	Query083
	Query084
	Query085
	Query086
	Query087
	Query088
	Query089
	Query090
	Query091
	Query092
	Query093
	Query094
	Query095
	Query096
	Query097
	Query098
	Query099
	Query100
	Query101
	Query102
	Query103
	Query104
	Query105
	Query106
	Query107
	Query108
	Query109
	Query110
	Query111
	Query112
	Query113
	Query114
	Query115
	Query116
	Query117
	Query118
	Query119
	Query120
	Query121
	Query122
	Query123
	Query124
	Query125
	Query126
	Query127
	Query128
	Query129
	Query130
	Query131
	Query132
	Query133
	Query134
	Query135
	Query136
	Query137
	Query138
	Query139
	Query140
	Query141
	Query142
	Query143
	Query144
	Query145
	Query146
	Query147
	Query148
	Query149
	Query150
	Query151
	Query152
	Query153
	Query154
	Query155
	Query156
	Query157
	Query158
	Query159
	Query160
	Query161
	Query162
	Query163
	Query164
	Query165
	Query166
	Query167
	Query168
	Query169
	Query170
	Query171
	Query172
	Query173
	Query174
	Query175
	Query176
	Query177
	Query178
	Query179
	Query180
	Query181
	Query182
	Query183
	Query184
	Query185
	Query186
	Query187
	Query188
	Query189
	Query190
	Query191
	Query192
	Query193
	Query194
	Query195
	Query196
	Query197
	Query198
	Query199
	Query200
	Query201
	Query202
	Query203
	Query204
	Query205
	Query206
	Query207
	Query208
	Query209
	Query210
	Query211
	Query212
	Query213
	Query214
	Query215
	Query216
	Query217
	Query218
	Query219
	Query220
	Query221
	Query222
	Query223
	Query224
	Query225
	Query226
	Query227
	Query228
	Query229
	Query230
	Query231
	Query232
	Query233
	Query234
	Query235
	Query236
	Query237
	Query238
	Query239
	Query240
	Query241
	Query242
	Query243
	Query244
	Query245
	Query246
	Query247
	Query248
	Query249
	Query250
	Query251
	Query252
	Query253
	Query254
	Query255
	Query256
	Query257
	Query258
	Query259
	Query260
	Query261
	Query262
	Query263
	Query264
	Query265
	Query266
	Query267
	Query268
	Query269
	Query270
	Query271
	Query272
	Query273
	Query274
	Query275
	Query276
	Query277
	Query278
	Query279
	Query280
	Query281
	Query282
	Query283
	Query284
	Query285
	Query286
	Query287
	Query288
)

func Exec(ctx context.Context, db testsql.DBTX, query Query, args ...any) (pgconn.CommandTag, error) {
	_, err := invoke(ctx, db, query, args)
	return pgconn.CommandTag{}, err
}

type Row struct {
	value any
	err   error
}

func QueryRow(ctx context.Context, db testsql.DBTX, query Query, args ...any) Row {
	value, err := invoke(ctx, db, query, args)
	return Row{value: value, err: err}
}

func (r Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	value := reflect.ValueOf(r.value)
	if value.Kind() == reflect.Struct {
		if value.NumField() != len(dest) {
			return fmt.Errorf("scan destinations %d != result fields %d", len(dest), value.NumField())
		}
		for i := range dest {
			if err := assign(dest[i], value.Field(i).Interface()); err != nil {
				return err
			}
		}
		return nil
	}
	if len(dest) != 1 {
		return fmt.Errorf("scan destinations %d != scalar result", len(dest))
	}
	return assign(dest[0], r.value)
}

func invoke(ctx context.Context, db testsql.DBTX, query Query, args []any) (any, error) {
	if query < Query001 || query > Query288 {
		return nil, fmt.Errorf("unknown test query %d", query)
	}
	method := reflect.ValueOf(testsql.New(db)).
		MethodByName(fmt.Sprintf("TestQuery%03d", query))
	if !method.IsValid() {
		return nil, fmt.Errorf("test query %d is not generated", query)
	}
	callArgs := []reflect.Value{reflect.ValueOf(ctx)}
	if method.Type().NumIn() == 2 {
		paramType := method.Type().In(1)
		if paramType.Kind() == reflect.Struct {
			param := reflect.New(paramType).Elem()
			if param.NumField() != len(args) {
				return nil, fmt.Errorf("test query %d arguments %d != fields %d", query, len(args), param.NumField())
			}
			for i, arg := range args {
				if err := set(param.Field(i), arg); err != nil {
					return nil, fmt.Errorf("test query %d argument %d: %w", query, i+1, err)
				}
			}
			callArgs = append(callArgs, param)
		} else {
			if len(args) != 1 {
				return nil, fmt.Errorf("test query %d arguments %d != 1", query, len(args))
			}
			value := reflect.New(paramType).Elem()
			if err := set(value, args[0]); err != nil {
				return nil, err
			}
			callArgs = append(callArgs, value)
		}
	} else if len(args) != 0 {
		return nil, fmt.Errorf("test query %d takes no arguments", query)
	}

	out := method.Call(callArgs)
	errValue := out[len(out)-1]
	if !errValue.IsNil() {
		return nil, errValue.Interface().(error)
	}
	if len(out) == 1 {
		return nil, nil
	}
	return out[0].Interface(), nil
}

func set(dst reflect.Value, source any) error {
	if source == nil {
		return nil
	}
	src := reflect.ValueOf(source)
	if dst.Type() == reflect.TypeOf(time.Time{}) && src.Kind() == reflect.String {
		value, err := time.Parse(time.RFC3339, src.String())
		if err != nil {
			return err
		}
		dst.Set(reflect.ValueOf(value))
		return nil
	}
	if dst.Kind() == reflect.Pointer && src.Kind() != reflect.Pointer {
		dst.Set(reflect.New(dst.Type().Elem()))
		return set(dst.Elem(), source)
	}
	if src.Kind() == reflect.Pointer && dst.Kind() != reflect.Pointer {
		if src.IsNil() {
			return nil
		}
		src = src.Elem()
	}
	if src.Type().AssignableTo(dst.Type()) {
		dst.Set(src)
		return nil
	}
	if src.Type().ConvertibleTo(dst.Type()) {
		dst.Set(src.Convert(dst.Type()))
		return nil
	}
	if dst.Kind() == reflect.Slice && src.Kind() != reflect.Slice {
		value := reflect.MakeSlice(dst.Type(), 1, 1)
		if err := set(value.Index(0), source); err != nil {
			return err
		}
		dst.Set(value)
		return nil
	}
	return fmt.Errorf("cannot assign %s to %s", src.Type(), dst.Type())
}

func assign(destination, source any) error {
	if scanner, ok := destination.(interface{ Scan(any) error }); ok {
		if value := reflect.ValueOf(source); value.IsValid() && value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return scanner.Scan(nil)
			}
			source = value.Elem().Interface()
		}
		return scanner.Scan(source)
	}
	dst := reflect.ValueOf(destination)
	if dst.Kind() != reflect.Pointer || dst.IsNil() {
		return errors.New("scan destination must be a non-nil pointer")
	}
	return set(dst.Elem(), source)
}
