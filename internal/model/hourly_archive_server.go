package model

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var mysqlHourlyInteger = regexp.MustCompile(`^(tinyint|smallint|mediumint|int|bigint)(\([0-9]+\))?( unsigned)?$`)
var mysqlHourlyText = regexp.MustCompile(`^(tinytext|text|mediumtext|longtext|varchar\([1-9][0-9]*\))$`)
var postgresHourlyText = regexp.MustCompile(`^(text|character varying\([1-9][0-9]*\))$`)

// Server archives retain the declared SQL type as well as the value. In
// particular PostgreSQL REAL must not acquire float64 rounding or lose its
// original float32 bits. Format 1 and its existing hashes remain unchanged.
func serverHourlyKind(source, column, sqlType string) (string, error) {
	kind := ""
	switch source {
	case "mysql-migration-18":
		switch {
		case mysqlHourlyInteger.MatchString(sqlType):
			kind = "integer"
		case mysqlHourlyText.MatchString(sqlType):
			kind = "text"
		case sqlType == "float":
			kind = "real32"
		case sqlType == "double":
			kind = "real"
		}
	case "postgres-migration-18":
		switch sqlType {
		case "smallint", "integer", "bigint":
			kind = "integer"
		case "real":
			kind = "real32"
		case "double precision":
			kind = "real"
		default:
			if postgresHourlyText.MatchString(sqlType) {
				kind = "text"
			}
		}
	}
	expected := "integer"
	switch column {
	case "date":
		expected = "text"
	case "input_cost", "output_cost":
		expected = kind
		if kind != "real" && kind != "real32" {
			expected = "float"
		}
	default:
		found := false
		for _, name := range HourlyArchiveColumns() {
			if name == column {
				found = true
			}
		}
		if !found {
			kind = ""
		}
	}
	if kind == "" || kind != expected {
		return "", fmt.Errorf("hourly quarantine requires review of unsupported column types")
	}
	return kind, nil
}

// NewServerHourlyArchiveCell consumes native server values. MySQL callers must
// use the binary prepared-statement protocol; PostgreSQL floats use float*send.
// No decimal text conversion or ORM zero-value normalization is permitted.
func NewServerHourlyArchiveCell(source, column, sqlType string, value any) (HourlyArchiveCell, error) {
	c := HourlyArchiveCell{Column: column, SQLType: sqlType, Type: "null"}
	kind, err := serverHourlyKind(source, column, sqlType)
	if err != nil {
		return c, err
	}
	if value == nil {
		return c, nil
	}
	c.Type = kind
	switch kind {
	case "integer":
		switch v := value.(type) {
		case int64:
			c.Value = strconv.FormatInt(v, 10)
		case uint64:
			c.Value = strconv.FormatUint(v, 10)
			if v > math.MaxInt64 {
				c.Type = "unsigned"
			}
		case []byte:
			if source != "mysql-migration-18" || !strings.HasSuffix(sqlType, " unsigned") {
				return c, fmt.Errorf("unexpected native hourly integer")
			}
			parsed, err := strconv.ParseUint(string(v), 10, 64)
			if err != nil {
				return c, fmt.Errorf("invalid native unsigned hourly integer")
			}
			c.Value = strconv.FormatUint(parsed, 10)
			if parsed > math.MaxInt64 {
				c.Type = "unsigned"
			}
		default:
			return c, fmt.Errorf("unexpected native hourly integer")
		}
	case "text":
		switch v := value.(type) {
		case string:
			c.Value = base64.StdEncoding.EncodeToString([]byte(v))
		case []byte:
			c.Value = base64.StdEncoding.EncodeToString(v)
		default:
			return c, fmt.Errorf("unexpected native hourly text")
		}
	case "real", "real32":
		if source == "postgres-migration-18" {
			bits, ok := value.([]byte)
			if !ok || (kind == "real32" && len(bits) != 4) || (kind == "real" && len(bits) != 8) {
				return c, fmt.Errorf("unexpected native hourly float bits")
			}
			if kind == "real32" {
				c.Value = fmt.Sprintf("%08x", binary.BigEndian.Uint32(bits))
			} else {
				c.Value = fmt.Sprintf("%016x", binary.BigEndian.Uint64(bits))
			}
		} else {
			if kind == "real32" {
				v, ok := value.(float32)
				if !ok {
					return c, fmt.Errorf("unexpected native hourly float32")
				}
				c.Value = fmt.Sprintf("%08x", math.Float32bits(v))
			} else {
				v, ok := value.(float64)
				if !ok {
					return c, fmt.Errorf("unexpected native hourly float64")
				}
				c.Value = fmt.Sprintf("%016x", math.Float64bits(v))
			}
		}
	}
	return c, validateServerHourlyCell(source, c)
}

func validateServerHourlyCell(source string, c HourlyArchiveCell) error {
	kind, err := serverHourlyKind(source, c.Column, c.SQLType)
	if err != nil {
		return err
	}
	bad := fmt.Errorf("invalid native hourly archive cell")
	if c.Type == "null" {
		if c.Value != "" || c.Column == "hour" {
			return bad
		}
		return nil
	}
	if c.Type == "unsigned" {
		v, e := strconv.ParseUint(c.Value, 10, 64)
		if kind != "integer" || !strings.HasSuffix(c.SQLType, " unsigned") || c.Column == "hour" || e != nil || v <= math.MaxInt64 || strconv.FormatUint(v, 10) != c.Value {
			return bad
		}
		return nil
	}
	if c.Type != kind {
		return bad
	}
	switch kind {
	case "integer":
		v, e := strconv.ParseInt(c.Value, 10, 64)
		if e != nil || strconv.FormatInt(v, 10) != c.Value || (strings.HasSuffix(c.SQLType, " unsigned") && v < 0) {
			return bad
		}
	case "text":
		v, e := base64.StdEncoding.DecodeString(c.Value)
		if e != nil || base64.StdEncoding.EncodeToString(v) != c.Value {
			return bad
		}
	case "real", "real32":
		bits, width := 64, 16
		if kind == "real32" {
			bits, width = 32, 8
		}
		v, e := strconv.ParseUint(c.Value, 16, bits)
		if e != nil || fmt.Sprintf("%0*x", width, v) != c.Value {
			return bad
		}
	}
	return nil
}
