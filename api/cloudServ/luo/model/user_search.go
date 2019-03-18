package model

import (
	"encoding/json"
	"io"
)

type UserSearch struct {
	Term           string `json:"term"`
	AllowInactive  bool   `json:"allow_inactive"`
}

// ToJson convert a User to a json string
func (u *UserSearch) ToJson() string {
	b, err := json.Marshal(u)
	if err != nil {
		return ""
	} else {
		return string(b)
	}
}

// UserSearchFromJson will decode the input and return a User
func UserSearchFromJson(data io.Reader) *UserSearch {
	decoder := json.NewDecoder(data)
	var us UserSearch
	err := decoder.Decode(&us)
	if err == nil {
		return &us
	} else {
		return nil
	}
}
