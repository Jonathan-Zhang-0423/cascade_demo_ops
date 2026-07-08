package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func New(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%x_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}