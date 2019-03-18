package sqlStore

import (
	"database/sql"
	"net/http"

	l4g "github.com/alecthomas/log4go"

	"github.com/teddy/sign-in-on/model"
)

type SqlTokenStore struct {
	SqlStore
}

func NewSqlTokenStore(sqlStore SqlStore) TokenStore {
	s := &SqlTokenStore{sqlStore}

	for _, db := range sqlStore.GetAllConns() {
		table := db.AddTableWithName(model.Token{}, "Tokens").SetKeys(false, "Token")
		table.ColMap("Token").SetMaxSize(64)
		table.ColMap("Type").SetMaxSize(64)
		table.ColMap("Extra").SetMaxSize(128)
	}

	return s
}

func (s SqlTokenStore) CreateIndexesIfNotExists() {
}

func (s SqlTokenStore) Save(token *model.Token) StoreChannel {

	storeChannel := make(StoreChannel, 1)

	go func() {
		result := StoreResult{}

		if result.Err = token.IsValid(); result.Err != nil {
			storeChannel <- result
			close(storeChannel)
			return
		}

		if err := s.GetMaster().Insert(token); err != nil {
			result.Err = model.NewLocAppError("SqlTokenStore.Save", "store.sql_recover.save.app_error", nil, "")
		}

		storeChannel <- result
		close(storeChannel)
	}()

	return storeChannel
}

func (s SqlTokenStore) Delete(token string) StoreChannel {

	storeChannel := make(StoreChannel, 1)

	go func() {
		result := StoreResult{}

		if _, err := s.GetMaster().Exec("DELETE FROM Tokens WHERE Token = :Token", map[string]interface{}{"Token": token}); err != nil {
			result.Err = model.NewLocAppError("SqlTokenStore.Delete", "store.sql_recover.delete.app_error", nil, "")
		}

		storeChannel <- result
		close(storeChannel)
	}()

	return storeChannel
}

func (s SqlTokenStore) GetByToken(tokenString string) StoreChannel {

	storeChannel := make(StoreChannel, 1)

	go func() {
		result := StoreResult{}

		token := model.Token{}

		if err := s.GetReplica().SelectOne(&token, "SELECT * FROM Tokens WHERE Token = :Token", map[string]interface{}{"Token": tokenString}); err != nil {
			if err == sql.ErrNoRows {
				result.Err = model.NewAppError("SqlTokenStore.GetByToken", "store.sql_recover.get_by_code.app_error", nil, err.Error(), http.StatusBadRequest)
			} else {
				result.Err = model.NewAppError("SqlTokenStore.GetByToken", "store.sql_recover.get_by_code.app_error", nil, err.Error(), http.StatusInternalServerError)
			}
		}

		result.Data = &token

		storeChannel <- result
		close(storeChannel)
	}()

	return storeChannel
}

func (s SqlTokenStore) GetByExtra(extra string) StoreChannel {

	storeChannel := make(StoreChannel, 1)

	go func() {
		result := StoreResult{}

		token := model.Token{}

		if err := s.GetReplica().SelectOne(&token, "SELECT * FROM Tokens WHERE Extra = :Extra ORDER BY CreateAt DESC limit 1", map[string]interface{}{"Extra": extra}); err != nil {
			if err == sql.ErrNoRows {
				result.Err = model.NewAppError("SqlTokenStore.GetByExtra", "store.sql_recover.get_by_extra.app_error", nil, err.Error(), http.StatusBadRequest)
			} else {
				result.Err = model.NewAppError("SqlTokenStore.GetByExtra", "store.sql_recover.get_by_extra.app_error", nil, err.Error(), http.StatusInternalServerError)
			}
		}

		result.Data = &token

		storeChannel <- result
		close(storeChannel)
	}()

	return storeChannel
}

func (s SqlTokenStore) GetTokenCountByExtra(extra string) StoreChannel {

	storeChannel := make(StoreChannel, 1)

	go func() {
		result := StoreResult{}

		count, err := s.GetReplica().SelectInt(`
			SELECT 
				count(*) 
			FROM 
				Tokens 
			WHERE 
				Extra = :Extra`, map[string]interface{}{"Extra": extra}) 
		if err != nil {
			result.Err = model.NewAppError("SqlTokenStore.GetTokenCountByExtra", "store.sql_token.get_token_count_by_extra.app_error", nil, err.Error(), http.StatusInternalServerError)
		}

		result.Data = count

		storeChannel <- result
		close(storeChannel)
	}()

	return storeChannel
}

func (s SqlTokenStore) Cleanup() {
	l4g.Debug("Cleaning up token store.")
	deltime := model.GetMillis() - model.MAX_TOKEN_EXIPRY_TIME
	if _, err := s.GetMaster().Exec("DELETE FROM Tokens WHERE CreateAt < :DelTime", map[string]interface{}{"DelTime": deltime}); err != nil {
		l4g.Error("Unable to cleanup token store.")
	}
}
