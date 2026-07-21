package model

import (
	"strings"
	"time"
)

type Username string

func (u Username) ToLower() Username {
	return Username(strings.ToLower(string(u)))
}

type UserId string

type Timestamp int64

func (t Timestamp) Time() time.Time {
	return time.UnixMilli(int64(t))
}
