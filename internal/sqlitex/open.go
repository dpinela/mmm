package sqlitex

import "github.com/dpinela/mmm/internal/sqlite"

func OpenWithSchema(filename string, schema string) (*sqlite.DB, error) {
	conn, err := sqlite.Open(filename)
	if err != nil {
		return nil, err
	}

	err = conn.Exec(schema)
	if err != nil {
		conn.Close()
		return nil, err
	}

	return conn, err
}
