package server

import "golang.org/x/crypto/bcrypt"

// bcryptCheck wraps bcrypt.CompareHashAndPassword for use in handlers.go.
func bcryptCheck(hash, password []byte) bool {
	return bcrypt.CompareHashAndPassword(hash, password) == nil
}
