package install

import "github.com/no-dal/ndl-ce/internal/auth"

func hashPassword(password string) (string, error) {
	return auth.HashPassword(password)
}
