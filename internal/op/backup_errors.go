package op

import (
	"errors"
	"fmt"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

var (
	ErrBackupInput     = errors.New("invalid backup; no changes were applied")
	ErrBackupConflict  = errors.New("backup conflicts with existing data; no changes were applied")
	ErrBackupCommitted = errors.New("backup was committed but caches could not be published; forwarding and statistics writes are paused; restart the service before continuing, do not repeat the import")
)

// Driver error strings can contain credentials or SQL literals. Only this safe
// context is returned to HTTP callers/logs; the cause remains available to tests.
type backupError struct {
	kind   error
	table  string
	id     int
	reason string
	cause  error
}

func (e *backupError) Error() string {
	return fmt.Sprintf("%s: %s ID %d: %s", e.kind, e.table, e.id, e.reason)
}
func (e *backupError) Is(target error) bool { return target == e.kind }
func (e *backupError) Unwrap() error        { return e.cause }
func backupProblem(kind error, table string, id int, reason string) error {
	return &backupError{kind: kind, table: table, id: id, reason: reason}
}
func backupDatabaseError(table string, id int, err error) error {
	kind, reason := ErrBackupWrite, "database operation failed"
	var pg *pgconn.PgError
	var my *mysql.MySQLError
	var sq interface{ Code() int }
	if errors.Is(err, gorm.ErrDuplicatedKey) || errors.As(err, &pg) && pg.Code == "23505" ||
		errors.As(err, &my) && my.Number == 1062 || errors.As(err, &sq) && (sq.Code() == 1555 || sq.Code() == 2067) {
		kind, reason = ErrBackupConflict, "unique key belongs to another row (including database collation)"
	}
	if errors.As(err, &pg) && pg.Code == "42501" {
		reason = "database permissions or sequence ownership are insufficient (SQLSTATE 42501)"
	}
	return &backupError{kind: kind, table: table, id: id, reason: reason, cause: err}
}
