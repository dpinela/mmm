package sqlite

// compiler flags recommended by the SQLite documentation at:
// https://www.sqlite.org/compile.html
// We don't need SQLite itself to be thread-safe, as we can manage concurrency
// internally.

// #cgo CFLAGS: -DSQLITE_DQS=0 -DSQLITE_THREADSAFE=0 -DSQLITE_DEFAULT_MEMSTATUS=0 -DSQLITE_DEFAULT_WAL_SYNCHRONOUS=1 -DSQLITE_LIKE_DOESNT_MATCH_BLOBS -DSQLITE_MAX_EXPR_DEPTH=0 -DSQLITE_OMIT_DECLTYPE -DSQLITE_OMIT_DEPRECATED -DSQLITE_OMIT_PROGRESS_CALLBACK -DSQLITE_OMIT_SHARED_CACHE -DSQLITE_USE_ALLOCA -DSQLITE_OMIT_AUTOINIT -DSQLITE_STRICT_SUBTYPE=1
// #include "sqlite3.h"
// #include <stdlib.h>
import "C"

import (
	"errors"
	"unsafe"
)

func init() {
	must(C.sqlite3_initialize())
}

type DB struct {
	conn *C.sqlite3
}

func Open(location string) (*DB, error) {
	db := &DB{}
	cLocation := C.CString(location)
	defer C.free(unsafe.Pointer(cLocation))
	err := C.sqlite3_open(cLocation, &db.conn)
	if err != C.SQLITE_OK {
		return nil, errorFromCode(err)
	}
	return db, nil
}

func (db *DB) Prepare(sql string) *Statement {
	s := &Statement{}
	must(C.sqlite3_prepare_v2(db.conn, cPointer(sql), C.int(len(sql)), &s.stmt, nil))
	return s
}

func (db *DB) Exec(sql string) error {
	cSchema := C.CString(sql)
	defer C.free(unsafe.Pointer(cSchema))
	var errMsg *C.char
	C.sqlite3_exec(db.conn, cSchema, nil, nil, &errMsg)
	if errMsg != nil {
		defer C.sqlite3_free(unsafe.Pointer(errMsg))
		return errors.New(C.GoString(errMsg))
	}
	return nil
}

func (db *DB) Close() {
	must(C.sqlite3_close(db.conn))
	db.conn = nil
}

type Statement struct {
	stmt *C.sqlite3_stmt
}

func (s *Statement) BindInt(param int, value int) {
	s.BindInt64(param, int64(value))
}

func (s *Statement) BindInt64(param int, value int64) {
	must(C.sqlite3_bind_int64(s.stmt, C.int(param), C.sqlite3_int64(value)))
}

func (s *Statement) BindString(param int, value string) {
	must(C.sqlite3_bind_text(s.stmt, C.int(param), cPointer(value), C.int(len(value)), C.SQLITE_TRANSIENT))
}

func (s *Statement) BindBytes(param int, value []byte) {
	must(C.sqlite3_bind_text(s.stmt, C.int(param), (*C.char)(unsafe.Pointer(unsafe.SliceData(value))), C.int(len(value)), C.SQLITE_TRANSIENT))
}

func (s *Statement) ReadInt32(column int) int {
	return int(C.sqlite3_column_int(s.stmt, C.int(column)))
}

func (s *Statement) ReadInt64(column int) int64 {
	return int64(C.sqlite3_column_int64(s.stmt, C.int(column)))
}

func (s *Statement) ReadBytes(column int) []byte {
	ptr, len := s.readText(column)
	return C.GoBytes(ptr, len)
}

func (s *Statement) ReadString(column int) string {
	ptr, len := s.readText(column)
	return C.GoStringN((*C.char)(ptr), len)
}

func (s *Statement) readText(column int) (unsafe.Pointer, C.int) {
	ptr := unsafe.Pointer(C.sqlite3_column_text(s.stmt, C.int(column)))
	len := C.sqlite3_column_bytes(s.stmt, C.int(column))
	return ptr, len
}

func (s *Statement) Step() (hasRow bool, err error) {
	switch code := C.sqlite3_step(s.stmt); code {
	case C.SQLITE_DONE:
		return false, nil
	case C.SQLITE_ROW:
		return true, nil
	case C.SQLITE_MISUSE:
		panic(C.GoString(C.sqlite3_errstr(code)))
	default:
		return false, errorFromCode(code)
	}
}

func (s *Statement) Exec() error {
	switch code := C.sqlite3_step(s.stmt); code {
	case C.SQLITE_DONE:
		return nil
	case C.SQLITE_MISUSE, C.SQLITE_ROW:
		panic(C.GoString(C.sqlite3_errstr(code)))
	default:
		return errorFromCode(code)
	}
}

func (s *Statement) Reset() error {
	if code := C.sqlite3_reset(s.stmt); code != C.SQLITE_OK {
		return errorFromCode(code)
	}
	return nil
}

func (s *Statement) Close() {
	must(C.sqlite3_finalize(s.stmt))
	s.stmt = nil
}

func cPointer(s string) *C.char {
	return (*C.char)(unsafe.Pointer(unsafe.StringData(s)))
}

func must(code C.int) {
	if code != C.SQLITE_OK {
		panic(C.GoString(C.sqlite3_errstr(code)))
	}
}

func errorFromCode(code C.int) error {
	return errors.New(C.GoString(C.sqlite3_errstr(code)))
}
