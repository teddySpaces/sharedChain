package sqlStore

import (
	"context"
	dbsql "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	sqltrace "log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	l4g "github.com/alecthomas/log4go"
	"github.com/mattermost/gorp"
	"github.com/teddy/sign-in-on/model"
	"github.com/teddy/sign-in-on/utils"
)

const (
	INDEX_TYPE_FULL_TEXT = "full_text"
	INDEX_TYPE_DEFAULT   = "default"
	MAX_DB_CONN_LIFETIME = 60
	DB_PING_ATTEMPTS     = 18
	DB_PING_TIMEOUT_SECS = 10
)

const (
	EXIT_CREATE_TABLE                = 100
	EXIT_DB_OPEN                     = 101
	EXIT_PING                        = 102
	EXIT_NO_DRIVER                   = 103
	EXIT_TABLE_EXISTS                = 104
	EXIT_TABLE_EXISTS_MYSQL          = 105
	EXIT_COLUMN_EXISTS               = 106
	EXIT_DOES_COLUMN_EXISTS_POSTGRES = 107
	EXIT_DOES_COLUMN_EXISTS_MYSQL    = 108
	EXIT_DOES_COLUMN_EXISTS_MISSING  = 109
	EXIT_CREATE_COLUMN_POSTGRES      = 110
	EXIT_CREATE_COLUMN_MYSQL         = 111
	EXIT_CREATE_COLUMN_MISSING       = 112
	EXIT_REMOVE_COLUMN               = 113
	EXIT_RENAME_COLUMN               = 114
	EXIT_MAX_COLUMN                  = 115
	EXIT_ALTER_COLUMN                = 116
	EXIT_CREATE_INDEX_POSTGRES       = 117
	EXIT_CREATE_INDEX_MYSQL          = 118
	EXIT_CREATE_INDEX_FULL_MYSQL     = 119
	EXIT_CREATE_INDEX_MISSING        = 120
	EXIT_REMOVE_INDEX_POSTGRES       = 121
	EXIT_REMOVE_INDEX_MYSQL          = 122
	EXIT_REMOVE_INDEX_MISSING        = 123
	EXIT_REMOVE_TABLE                = 134
)

type SqlSupplierResult struct {
	Err    model.AppError
	Result interface{}
}

type SqlSupplierOldStores struct {
	user   UserStore
	system SystemStore
	token  TokenStore
}

type SqlSupplier struct {
	master         *gorp.DbMap
	replicas       []*gorp.DbMap
	searchReplicas []*gorp.DbMap
	rrCounter      int64
	srCounter      int64
	oldStores      SqlSupplierOldStores
}

func NewSqlSupplier() *SqlSupplier {
	supplier := &SqlSupplier{
		rrCounter: 0,
		srCounter: 0,
	}

	supplier.initConnection()

	supplier.oldStores.user = NewSqlUserStore(supplier)
	supplier.oldStores.system = NewSqlSystemStore(supplier)
	supplier.oldStores.token = NewSqlTokenStore(supplier)

	err := supplier.GetMaster().CreateTablesIfNotExists()
	if err != nil {
		l4g.Critical(utils.T("store.sql.creating_tables.critical"), err)
		time.Sleep(time.Second)
		os.Exit(EXIT_CREATE_TABLE)
	}
	UpgradeDatabase(supplier)

	supplier.oldStores.user.(*SqlUserStore).CreateIndexesIfNotExists()
	supplier.oldStores.system.(*SqlSystemStore).CreateIndexesIfNotExists()
	supplier.oldStores.token.(*SqlTokenStore).CreateIndexesIfNotExists()

	return supplier
}

func setupConnection(con_type string, driver string, dataSource string, maxIdle int, maxOpen int, trace bool) *gorp.DbMap {
	db, err := dbsql.Open(driver, dataSource)
	if err != nil {
		l4g.Critical(utils.T("store.sql.open_conn.critical"), err)
		time.Sleep(time.Second)
		os.Exit(EXIT_DB_OPEN)
	}

	for i := 0; i < DB_PING_ATTEMPTS; i++ {
		l4g.Info("Pinging SQL %v database", con_type)
		ctx, cancel := context.WithTimeout(context.Background(), DB_PING_TIMEOUT_SECS*time.Second)
		defer cancel()
		err = db.PingContext(ctx)
		if err == nil {
			break
		} else {
			if i == DB_PING_ATTEMPTS-1 {
				l4g.Critical("Failed to ping DB, server will exit err=%v", err)
				time.Sleep(time.Second)
				os.Exit(EXIT_PING)
			} else {
				l4g.Error("Failed to ping DB retrying in %v seconds err=%v", DB_PING_TIMEOUT_SECS, err)
				time.Sleep(DB_PING_TIMEOUT_SECS * time.Second)
			}
		}
	}

	db.SetMaxIdleConns(maxIdle)
	db.SetMaxOpenConns(maxOpen)
	db.SetConnMaxLifetime(time.Duration(MAX_DB_CONN_LIFETIME) * time.Minute)

	var dbmap *gorp.DbMap

	connectionTimeout := time.Duration(*utils.Cfg.SqlSettings.QueryTimeout) * time.Second

	if driver == "sqlite3" {
		dbmap = &gorp.DbMap{Db: db, TypeConverter: mattermConverter{}, Dialect: gorp.SqliteDialect{}, QueryTimeout: connectionTimeout}
	} else if driver == model.DATABASE_DRIVER_MYSQL {
		dbmap = &gorp.DbMap{Db: db, TypeConverter: mattermConverter{}, Dialect: gorp.MySQLDialect{Engine: "InnoDB", Encoding: "UTF8MB4"}, QueryTimeout: connectionTimeout}
	} else {
		l4g.Critical(utils.T("store.sql.dialect_driver.critical"))
		time.Sleep(time.Second)
		os.Exit(EXIT_NO_DRIVER)
	}

	if trace {
		dbmap.TraceOn("", sqltrace.New(os.Stdout, "sql-trace:", sqltrace.Lmicroseconds))
	}

	return dbmap
}

func (s *SqlSupplier) initConnection() {
	s.master = setupConnection("master", utils.Cfg.SqlSettings.DriverName,
		utils.Cfg.SqlSettings.DataSource, utils.Cfg.SqlSettings.MaxIdleConns,
		utils.Cfg.SqlSettings.MaxOpenConns, utils.Cfg.SqlSettings.Trace)

	if len(utils.Cfg.SqlSettings.DataSourceReplicas) == 0 {
		s.replicas = make([]*gorp.DbMap, 1)
		s.replicas[0] = s.master
	} else {
		s.replicas = make([]*gorp.DbMap, len(utils.Cfg.SqlSettings.DataSourceReplicas))
		for i, replica := range utils.Cfg.SqlSettings.DataSourceReplicas {
			s.replicas[i] = setupConnection(fmt.Sprintf("replica-%v", i), utils.Cfg.SqlSettings.DriverName, replica,
				utils.Cfg.SqlSettings.MaxIdleConns, utils.Cfg.SqlSettings.MaxOpenConns,
				utils.Cfg.SqlSettings.Trace)
		}
	}

	if len(utils.Cfg.SqlSettings.DataSourceSearchReplicas) == 0 {
		s.searchReplicas = s.replicas
	} else {
		s.searchReplicas = make([]*gorp.DbMap, len(utils.Cfg.SqlSettings.DataSourceSearchReplicas))
		for i, replica := range utils.Cfg.SqlSettings.DataSourceSearchReplicas {
			s.searchReplicas[i] = setupConnection(fmt.Sprintf("search-replica-%v", i), utils.Cfg.SqlSettings.DriverName, replica,
				utils.Cfg.SqlSettings.MaxIdleConns, utils.Cfg.SqlSettings.MaxOpenConns,
				utils.Cfg.SqlSettings.Trace)
		}
	}
}

func (ss *SqlSupplier) GetCurrentSchemaVersion() string {
	version, _ := ss.GetMaster().SelectStr("SELECT Value FROM Systems WHERE Name='Version'")
	return version
}

func (ss *SqlSupplier) GetMaster() *gorp.DbMap {
	return ss.master
}

func (ss *SqlSupplier) GetSearchReplica() *gorp.DbMap {
	rrNum := atomic.AddInt64(&ss.srCounter, 1) % int64(len(ss.searchReplicas))
	return ss.searchReplicas[rrNum]
}

func (ss *SqlSupplier) GetReplica() *gorp.DbMap {
	rrNum := atomic.AddInt64(&ss.rrCounter, 1) % int64(len(ss.replicas))
	return ss.replicas[rrNum]
}

func (ss *SqlSupplier) TotalMasterDbConnections() int {
	return ss.GetMaster().Db.Stats().OpenConnections
}

func (ss *SqlSupplier) TotalReadDbConnections() int {

	if len(utils.Cfg.SqlSettings.DataSourceReplicas) == 0 {
		return 0
	}

	count := 0
	for _, db := range ss.replicas {
		count = count + db.Db.Stats().OpenConnections
	}

	return count
}

func (ss *SqlSupplier) TotalSearchDbConnections() int {
	if len(utils.Cfg.SqlSettings.DataSourceSearchReplicas) == 0 {
		return 0
	}

	count := 0
	for _, db := range ss.searchReplicas {
		count = count + db.Db.Stats().OpenConnections
	}

	return count
}

func (ss *SqlSupplier) MarkSystemRanUnitTests() {
	if result := <-ss.System().Get(); result.Err == nil {
		props := result.Data.(model.StringMap)
		unitTests := props[model.SYSTEM_RAN_UNIT_TESTS]
		if len(unitTests) == 0 {
			systemTests := &model.System{Name: model.SYSTEM_RAN_UNIT_TESTS, Value: "1"}
			<-ss.System().Save(systemTests)
		}
	}
}

func (ss *SqlSupplier) DoesTableExist(tableName string) bool {
	if utils.Cfg.SqlSettings.DriverName == model.DATABASE_DRIVER_MYSQL {

		count, err := ss.GetMaster().SelectInt(
			`SELECT
		    COUNT(0) AS table_exists
			FROM
			    information_schema.TABLES
			WHERE
			    TABLE_SCHEMA = DATABASE()
			        AND TABLE_NAME = ?
		    `,
			tableName,
		)

		if err != nil {
			l4g.Critical(utils.T("store.sql.table_exists.critical"), err)
			time.Sleep(time.Second)
			os.Exit(EXIT_TABLE_EXISTS_MYSQL)
		}

		return count > 0

	} else {
		l4g.Critical(utils.T("store.sql.column_exists_missing_driver.critical"))
		time.Sleep(time.Second)
		os.Exit(EXIT_COLUMN_EXISTS)
		return false
	}
}

func (ss *SqlSupplier) DoesColumnExist(tableName string, columnName string) bool {
	if utils.Cfg.SqlSettings.DriverName == model.DATABASE_DRIVER_MYSQL {

		count, err := ss.GetMaster().SelectInt(
			`SELECT
		    COUNT(0) AS column_exists
		FROM
		    information_schema.COLUMNS
		WHERE
		    TABLE_SCHEMA = DATABASE()
		        AND TABLE_NAME = ?
		        AND COLUMN_NAME = ?`,
			tableName,
			columnName,
		)

		if err != nil {
			l4g.Critical(utils.T("store.sql.column_exists.critical"), err)
			time.Sleep(time.Second)
			os.Exit(EXIT_DOES_COLUMN_EXISTS_MYSQL)
		}

		return count > 0

	} else {
		l4g.Critical(utils.T("store.sql.column_exists_missing_driver.critical"))
		time.Sleep(time.Second)
		os.Exit(EXIT_DOES_COLUMN_EXISTS_MISSING)
		return false
	}
}

func (ss *SqlSupplier) CreateColumnIfNotExists(tableName string, columnName string, mySqlColType string, postgresColType string, defaultValue string) bool {

	if ss.DoesColumnExist(tableName, columnName) {
		return false
	}

	if utils.Cfg.SqlSettings.DriverName == model.DATABASE_DRIVER_MYSQL {
		_, err := ss.GetMaster().ExecNoTimeout("ALTER TABLE " + tableName + " ADD " + columnName + " " + mySqlColType + " DEFAULT '" + defaultValue + "'")
		if err != nil {
			l4g.Critical(utils.T("store.sql.create_column.critical"), err)
			time.Sleep(time.Second)
			os.Exit(EXIT_CREATE_COLUMN_MYSQL)
		}

		return true

	} else {
		l4g.Critical(utils.T("store.sql.create_column_missing_driver.critical"))
		time.Sleep(time.Second)
		os.Exit(EXIT_CREATE_COLUMN_MISSING)
		return false
	}
}

func (ss *SqlSupplier) RemoveColumnIfExists(tableName string, columnName string) bool {

	if !ss.DoesColumnExist(tableName, columnName) {
		return false
	}

	_, err := ss.GetMaster().ExecNoTimeout("ALTER TABLE " + tableName + " DROP COLUMN " + columnName)
	if err != nil {
		l4g.Critical("Failed to drop column %v", err)
		time.Sleep(time.Second)
		os.Exit(EXIT_REMOVE_COLUMN)
	}

	return true
}

func (ss *SqlSupplier) RemoveTableIfExists(tableName string) bool {
	if !ss.DoesTableExist(tableName) {
		return false
	}

	_, err := ss.GetMaster().ExecNoTimeout("DROP TABLE " + tableName)
	if err != nil {
		l4g.Critical("Failed to drop table %v", err)
		time.Sleep(time.Second)
		os.Exit(EXIT_REMOVE_TABLE)
	}

	return true
}

func (ss *SqlSupplier) RenameColumnIfExists(tableName string, oldColumnName string, newColumnName string, colType string) bool {
	if !ss.DoesColumnExist(tableName, oldColumnName) {
		return false
	}

	var err error
	if utils.Cfg.SqlSettings.DriverName == model.DATABASE_DRIVER_MYSQL {
		_, err = ss.GetMaster().ExecNoTimeout("ALTER TABLE " + tableName + " CHANGE " + oldColumnName + " " + newColumnName + " " + colType)
	}

	if err != nil {
		l4g.Critical(utils.T("store.sql.rename_column.critical"), err)
		time.Sleep(time.Second)
		os.Exit(EXIT_RENAME_COLUMN)
	}

	return true
}

func (ss *SqlSupplier) GetMaxLengthOfColumnIfExists(tableName string, columnName string) string {
	if !ss.DoesColumnExist(tableName, columnName) {
		return ""
	}

	var result string
	var err error
	if utils.Cfg.SqlSettings.DriverName == model.DATABASE_DRIVER_MYSQL {
		result, err = ss.GetMaster().SelectStr("SELECT CHARACTER_MAXIMUM_LENGTH FROM information_schema.columns WHERE table_name = '" + tableName + "' AND COLUMN_NAME = '" + columnName + "'")
	}

	if err != nil {
		l4g.Critical(utils.T("store.sql.maxlength_column.critical"), err)
		time.Sleep(time.Second)
		os.Exit(EXIT_MAX_COLUMN)
	}

	return result
}

func (ss *SqlSupplier) AlterColumnTypeIfExists(tableName string, columnName string, mySqlColType string, postgresColType string) bool {
	if !ss.DoesColumnExist(tableName, columnName) {
		return false
	}

	var err error
	if utils.Cfg.SqlSettings.DriverName == model.DATABASE_DRIVER_MYSQL {
		_, err = ss.GetMaster().ExecNoTimeout("ALTER TABLE " + tableName + " MODIFY " + columnName + " " + mySqlColType)
	}

	if err != nil {
		l4g.Critical(utils.T("store.sql.alter_column_type.critical"), err)
		time.Sleep(time.Second)
		os.Exit(EXIT_ALTER_COLUMN)
	}

	return true
}

func (ss *SqlSupplier) CreateUniqueIndexIfNotExists(indexName string, tableName string, columnName string) bool {
	return ss.createIndexIfNotExists(indexName, tableName, columnName, INDEX_TYPE_DEFAULT, true)
}

func (ss *SqlSupplier) CreateIndexIfNotExists(indexName string, tableName string, columnName string) bool {
	return ss.createIndexIfNotExists(indexName, tableName, columnName, INDEX_TYPE_DEFAULT, false)
}

func (ss *SqlSupplier) CreateFullTextIndexIfNotExists(indexName string, tableName string, columnName string) bool {
	return ss.createIndexIfNotExists(indexName, tableName, columnName, INDEX_TYPE_FULL_TEXT, false)
}

func (ss *SqlSupplier) createIndexIfNotExists(indexName string, tableName string, columnName string, indexType string, unique bool) bool {

	uniqueStr := ""
	if unique {
		uniqueStr = "UNIQUE "
	}

	if utils.Cfg.SqlSettings.DriverName == model.DATABASE_DRIVER_MYSQL {

		count, err := ss.GetMaster().SelectInt("SELECT COUNT(0) AS index_exists FROM information_schema.statistics WHERE TABLE_SCHEMA = DATABASE() and table_name = ? AND index_name = ?", tableName, indexName)
		if err != nil {
			l4g.Critical(utils.T("store.sql.check_index.critical"), err)
			time.Sleep(time.Second)
			os.Exit(EXIT_CREATE_INDEX_MYSQL)
		}

		if count > 0 {
			return false
		}

		fullTextIndex := ""
		if indexType == INDEX_TYPE_FULL_TEXT {
			fullTextIndex = " FULLTEXT "
		}

		_, err = ss.GetMaster().ExecNoTimeout("CREATE  " + uniqueStr + fullTextIndex + " INDEX " + indexName + " ON " + tableName + " (" + columnName + ")")
		if err != nil {
			l4g.Critical(utils.T("store.sql.create_index.critical"), err)
			time.Sleep(time.Second)
			os.Exit(EXIT_CREATE_INDEX_FULL_MYSQL)
		}
	} else {
		l4g.Critical(utils.T("store.sql.create_index_missing_driver.critical"))
		time.Sleep(time.Second)
		os.Exit(EXIT_CREATE_INDEX_MISSING)
	}

	return true
}

func (ss *SqlSupplier) RemoveIndexIfExists(indexName string, tableName string) bool {

	if utils.Cfg.SqlSettings.DriverName == model.DATABASE_DRIVER_MYSQL {

		count, err := ss.GetMaster().SelectInt("SELECT COUNT(0) AS index_exists FROM information_schema.statistics WHERE TABLE_SCHEMA = DATABASE() and table_name = ? AND index_name = ?", tableName, indexName)
		if err != nil {
			l4g.Critical(utils.T("store.sql.check_index.critical"), err)
			time.Sleep(time.Second)
			os.Exit(EXIT_REMOVE_INDEX_MYSQL)
		}

		if count <= 0 {
			return false
		}

		_, err = ss.GetMaster().ExecNoTimeout("DROP INDEX " + indexName + " ON " + tableName)
		if err != nil {
			l4g.Critical(utils.T("store.sql.remove_index.critical"), err)
			time.Sleep(time.Second)
			os.Exit(EXIT_REMOVE_INDEX_MYSQL)
		}
	} else {
		l4g.Critical(utils.T("store.sql.create_index_missing_driver.critical"))
		time.Sleep(time.Second)
		os.Exit(EXIT_REMOVE_INDEX_MISSING)
	}

	return true
}

func IsUniqueConstraintError(err string, indexName []string) bool {
	unique := strings.Contains(err, "unique constraint") || strings.Contains(err, "Duplicate entry")
	field := false
	for _, contain := range indexName {
		if strings.Contains(err, contain) {
			field = true
			break
		}
	}

	return unique && field
}

func (ss *SqlSupplier) GetAllConns() []*gorp.DbMap {
	all := make([]*gorp.DbMap, len(ss.replicas)+1)
	copy(all, ss.replicas)
	all[len(ss.replicas)] = ss.master
	return all
}

func (ss *SqlSupplier) Close() {
	l4g.Info(utils.T("store.sql.closing.info"))
	ss.master.Db.Close()
	for _, replica := range ss.replicas {
		replica.Db.Close()
	}
}

func (ss *SqlSupplier) User() UserStore {
	return ss.oldStores.user
}

func (ss *SqlSupplier) System() SystemStore {
	return ss.oldStores.system
}

func (ss *SqlSupplier) Token() TokenStore {
	return ss.oldStores.token
}

func (ss *SqlSupplier) DropAllTables() {
	ss.master.TruncateTables()
}

type mattermConverter struct{}

func (me mattermConverter) ToDb(val interface{}) (interface{}, error) {

	switch t := val.(type) {
	case model.StringMap:
		return model.MapToJson(t), nil
	case model.StringArray:
		return model.ArrayToJson(t), nil
	case model.StringInterface:
		return model.StringInterfaceToJson(t), nil
	case map[string]interface{}:
		return model.StringInterfaceToJson(model.StringInterface(t)), nil
	}

	return val, nil
}

func (me mattermConverter) FromDb(target interface{}) (gorp.CustomScanner, bool) {
	switch target.(type) {
	case *model.StringMap:
		binder := func(holder, target interface{}) error {
			s, ok := holder.(*string)
			if !ok {
				return errors.New(utils.T("store.sql.convert_string_map"))
			}
			b := []byte(*s)
			return json.Unmarshal(b, target)
		}
		return gorp.CustomScanner{Holder: new(string), Target: target, Binder: binder}, true
	case *model.StringArray:
		binder := func(holder, target interface{}) error {
			s, ok := holder.(*string)
			if !ok {
				return errors.New(utils.T("store.sql.convert_string_array"))
			}
			b := []byte(*s)
			return json.Unmarshal(b, target)
		}
		return gorp.CustomScanner{Holder: new(string), Target: target, Binder: binder}, true
	case *model.StringInterface:
		binder := func(holder, target interface{}) error {
			s, ok := holder.(*string)
			if !ok {
				return errors.New(utils.T("store.sql.convert_string_interface"))
			}
			b := []byte(*s)
			return json.Unmarshal(b, target)
		}
		return gorp.CustomScanner{Holder: new(string), Target: target, Binder: binder}, true
	case *map[string]interface{}:
		binder := func(holder, target interface{}) error {
			s, ok := holder.(*string)
			if !ok {
				return errors.New(utils.T("store.sql.convert_string_interface"))
			}
			b := []byte(*s)
			return json.Unmarshal(b, target)
		}
		return gorp.CustomScanner{Holder: new(string), Target: target, Binder: binder}, true
	}

	return gorp.CustomScanner{}, false
}

func convertMySQLFullTextColumnsToPostgres(columnNames string) string {
	columns := strings.Split(columnNames, ", ")
	concatenatedColumnNames := ""
	for i, c := range columns {
		concatenatedColumnNames += c
		if i < len(columns)-1 {
			concatenatedColumnNames += " || ' ' || "
		}
	}

	return concatenatedColumnNames
}
