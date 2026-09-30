package datamigrate

import (
	"bytes"
	"fmt"
)

// readCSV parses RFC 4180 CSV. It exists because encoding/csv rewrites \r\n inside a
// quoted field to \n, which would silently alter multi-line notes; here every byte inside
// quotes is kept. Rows may differ in length. Blank lines are skipped.
func readCSV(data []byte) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var rows [][]string
	var row []string
	var field []byte
	inQuotes, rowQuoted := false, false
	endRow := func() {
		row = append(row, string(field))
		field = nil
		if !(len(row) == 1 && row[0] == "" && !rowQuoted) {
			rows = append(rows, row)
		}
		row, rowQuoted = nil, false
	}
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inQuotes {
			switch {
			case c != '"':
				field = append(field, c)
			case i+1 < len(data) && data[i+1] == '"':
				field = append(field, '"')
				i++
			default:
				inQuotes = false
			}
			continue
		}
		switch c {
		case '"':
			if len(field) > 0 {
				return nil, fmt.Errorf("record %d: stray quote inside an unquoted field", len(rows)+1)
			}
			inQuotes, rowQuoted = true, true
		case ',':
			row = append(row, string(field))
			field = nil
		case '\r', '\n':
			if c == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				i++
			}
			endRow()
		default:
			field = append(field, c)
		}
	}
	if inQuotes {
		return nil, fmt.Errorf("record %d: unterminated quoted field", len(rows)+1)
	}
	if len(field) > 0 || len(row) > 0 {
		endRow()
	}
	return rows, nil
}
