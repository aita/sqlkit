package mysql

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/pingcap/tidb/pkg/parser/ast"
)

func convertUse(s *ast.UseStmt) (sql.Query, error) {
	return sql.Use(s.DBName), nil
}

func convertUnlockTables(*ast.UnlockTablesStmt) (sql.Query, error) {
	return sql.UnlockTables(), nil
}

func convertLockTables(s *ast.LockTablesStmt) (sql.Query, error) {
	locks := make([]sql.TableLock, 0, len(s.TableLocks))
	for _, l := range s.TableLocks {
		mode, err := tableLockMode(l.Type)
		if err != nil {
			return nil, err
		}
		locks = append(locks, sql.TableLock{Table: tableFromName(l.Table), Mode: mode})
	}
	return sql.LockTables(locks...), nil
}

// tableLockMode maps the READ/WRITE lock kinds sqlkit models; the LOCAL and
// READ ONLY variants have no typed home and round-trip raw.
func tableLockMode(t ast.TableLockType) (sql.TableLockMode, error) {
	switch t {
	case ast.TableLockRead:
		return sql.LockRead, nil
	case ast.TableLockWrite:
		return sql.LockWrite, nil
	default:
		return "", sqlparse.Unsupportedf("table lock type %s", t.String())
	}
}
