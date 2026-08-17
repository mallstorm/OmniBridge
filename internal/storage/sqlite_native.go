package storage

type nativeConn interface {
	exec(sql string) error
	query(sql string) ([]Row, error)
	close() error
}
